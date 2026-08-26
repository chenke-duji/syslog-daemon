package forward

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"syslog-daemon/internal/model"
)

// fakeForwarder records batches/singles sent for assertions.
type fakeForwarder struct {
	mu         sync.Mutex
	batches    [][]model.RawEvent
	singles    int
	failBatch  bool
	forwarded  int
}

func (f *fakeForwarder) ForwardBatch(_ context.Context, events []model.RawEvent) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.forwarded += len(events)
	cp := make([]model.RawEvent, len(events))
	copy(cp, events)
	f.batches = append(f.batches, cp)
	if f.failBatch {
		return &testErr{"batch failed"}
	}
	return nil
}

func (f *fakeForwarder) ForwardSingle(_ context.Context, event *model.RawEvent) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.singles++
	f.forwarded++
	return nil
}

func (f *fakeForwarder) Close() error { return nil }

func (f *fakeForwarder) stats() (int, int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.forwarded, f.singles
}

type testErr struct{ msg string }

func (e *testErr) Error() string { return e.msg }

// countingRecorder records counters for assertions.
type countingRecorder struct {
	mu       sync.Mutex
	received int
	forward  int
	failed   int
	dropped  int
}

func (c *countingRecorder) IncReceived() { c.mu.Lock(); c.received++; c.mu.Unlock() }
func (c *countingRecorder) IncForwarded(n int) {
	c.mu.Lock(); c.forward += n; c.mu.Unlock()
}
func (c *countingRecorder) IncForwardFailed(n int) {
	c.mu.Lock(); c.failed += n; c.mu.Unlock()
}
func (c *countingRecorder) IncDropped(n int) {
	c.mu.Lock(); c.dropped += n; c.mu.Unlock()
}

func (c *countingRecorder) get() (received, forwarded, failed, dropped int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.received, c.forward, c.failed, c.dropped
}

func testEvent(i int) *model.RawEvent {
	return &model.RawEvent{
		Source:          model.SourceSyslog,
		SourceIP:        "192.0.2.10",
		ReceivedAt:      int64(i),
		OriginTimestamp: int64(i),
		RawEvent:        "syslog from 192.0.2.10\nfacility: 1 severity: 5\nmessage: test message",
		Metadata: map[string]interface{}{
			"facility": 1,
			"severity": 5,
			"hostname": "gw01",
			"tag":      "ospfd",
			"message":  "test message",
		},
	}
}

func TestBatchQueueBatchesAndFlushes(t *testing.T) {
	cfg := ForwardConfig{
		BatchSize:          5,
		BatchFlushInterval: 50,
		Workers:            2,
		QueueCapacity:      100,
		QueueFullPolicy:    string(PolicyDrop),
		DropLogEnabled:     true,
	}
	fake := &fakeForwarder{}
	rec := &countingRecorder{}
	q := NewBatchQueue(cfg, fake, rec, slog.New(slog.NewTextHandler(io.Discard, nil)))
	q.Start()

	// Enqueue exactly batchSize so a single batch is forwarded.
	for i := 0; i < 5; i++ {
		if !q.Enqueue(testEvent(i)) {
			t.Fatal("enqueue unexpectedly dropped")
		}
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		got, _ := fake.stats()
		if got >= 5 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	q.Close()

	forwarded, _ := fake.stats()
	if forwarded < 5 {
		t.Fatalf("expected >=5 forwarded, got %d", forwarded)
	}
	received, _, _, _ := rec.get()
	if received != 5 {
		t.Fatalf("expected 5 received, got %d", received)
	}
}

func TestBatchQueueFlushOnInterval(t *testing.T) {
	cfg := ForwardConfig{
		BatchSize:          100, // larger than enqueued, so flush by timer
		BatchFlushInterval: 100,
		Workers:            1,
		QueueCapacity:      100,
		QueueFullPolicy:    string(PolicyDrop),
		DropLogEnabled:     true,
	}
	fake := &fakeForwarder{}
	rec := &countingRecorder{}
	q := NewBatchQueue(cfg, fake, rec, slog.New(slog.NewTextHandler(io.Discard, nil)))
	q.Start()
	q.Enqueue(testEvent(1))
	q.Enqueue(testEvent(2))

	time.Sleep(300 * time.Millisecond)
	q.Close()

	forwarded, _ := fake.stats()
	if forwarded != 2 {
		t.Fatalf("expected 2 forwarded by timer flush, got %d", forwarded)
	}
}

func TestBatchQueueDropWhenFull(t *testing.T) {
	cfg := ForwardConfig{
		BatchSize:          1,
		BatchFlushInterval: 1000,
		Workers:            1,
		QueueCapacity:      2, // small to force full
		QueueFullPolicy:    string(PolicyDrop),
		DropLogEnabled:     true,
	}
	fake := &fakeForwarder{}
	rec := &countingRecorder{}
	q := NewBatchQueue(cfg, fake, rec, slog.New(slog.NewTextHandler(io.Discard, nil)))
	q.Start()

	// Fill the queue and overflow it.
	for i := 0; i < 5; i++ {
		q.Enqueue(testEvent(i))
	}
	q.Close()

	received, forwarded, failed, dropped := rec.get()
	_ = received
	_ = forwarded
	_ = failed
	if dropped == 0 {
		t.Fatal("expected at least one drop when queue full")
	}
}

func TestBatchQueueFailForwardRecords(t *testing.T) {
	cfg := ForwardConfig{
		BatchSize:          1,
		BatchFlushInterval: 10,
		Workers:            1,
		QueueCapacity:      10,
		QueueFullPolicy:    string(PolicyDrop),
		DropLogEnabled:     true,
	}
	fake := &fakeForwarder{failBatch: true}
	rec := &countingRecorder{}
	q := NewBatchQueue(cfg, fake, rec, slog.New(slog.NewTextHandler(io.Discard, nil)))
	q.Start()
	q.Enqueue(testEvent(1))
	time.Sleep(50 * time.Millisecond)
	q.Close()

	_, _, failed, _ := rec.get()
	if failed == 0 {
		t.Fatal("expected forward failure to be recorded")
	}
}
