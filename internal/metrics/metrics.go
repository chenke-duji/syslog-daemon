// Package metrics implements the self-monitoring counters exposed to
// Prometheus. It records receive/forward/failed/dropped totals plus a sliding
// 5-minute throughput gauge, and renders the Prometheus text format over HTTP
// without pulling in a heavy client library.
package metrics

import (
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"
	"time"
)

// slidingWindowSeconds is the length of the throughput window (5 minutes).
const slidingWindowSeconds = 300

// Metrics implements forward.Recorder and serves a Prometheus /metrics text
// endpoint. All counters are thread-safe.
type Metrics struct {
	startTime time.Time

	received  atomic.Uint64
	forwarded atomic.Uint64
	failed    atomic.Uint64
	dropped   atomic.Uint64

	// Sliding window for throughput: one slot per wall-clock second.
	winMu    sync.Mutex
	winStamp []int64  // unix second per slot
	winCount []uint64 // received count per slot

	queueDepth func() int
}

// New creates a Metrics instance. queueDepth is called when rendering the
// metrics text to report the current forwarding queue depth.
func New(queueDepth func() int) *Metrics {
	return &Metrics{
		startTime:  time.Now(),
		winStamp:   make([]int64, slidingWindowSeconds),
		winCount:   make([]uint64, slidingWindowSeconds),
		queueDepth: queueDepth,
	}
}

// IncReceived records one received syslog message.
func (m *Metrics) IncReceived() {
	m.received.Add(1)
	now := time.Now().Unix()
	idx := now % slidingWindowSeconds
	m.winMu.Lock()
	if m.winStamp[idx] != now {
		m.winStamp[idx] = now
		m.winCount[idx] = 1
	} else {
		m.winCount[idx]++
	}
	m.winMu.Unlock()
}

// IncForwarded records n successfully forwarded events.
func (m *Metrics) IncForwarded(n uint64) { m.forwarded.Add(n) }

// IncForwardFailed records n failed forward attempts.
func (m *Metrics) IncForwardFailed(n uint64) { m.failed.Add(n) }

// IncDropped records n dropped events.
func (m *Metrics) IncDropped(n uint64) { m.dropped.Add(n) }

// through5m computes the average received events/sec over the last 5 minutes.
func (m *Metrics) through5m() float64 {
	now := time.Now().Unix()
	floor := now - slidingWindowSeconds + 1
	var sum uint64
	var slots int64
	m.winMu.Lock()
	for i := 0; i < slidingWindowSeconds; i++ {
		if m.winStamp[i] >= floor && m.winStamp[i] <= now {
			sum += m.winCount[i]
			slots++
		}
	}
	m.winMu.Unlock()
	if slots == 0 {
		return 0
	}
	return float64(sum) / float64(slidingWindowSeconds)
}

// Handler returns an http.Handler that renders the metrics in Prometheus text
// format (content-type: text/plain; version=0.0.4).
func (m *Metrics) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		depth := 0
		if m.queueDepth != nil {
			depth = m.queueDepth()
		}
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		lines := []string{
			"# HELP syslog_received_total Total syslog messages received.",
			"# TYPE syslog_received_total counter",
			fmt.Sprintf("syslog_received_total %d", m.received.Load()),
			"# HELP syslog_forward_total Total events successfully forwarded.",
			"# TYPE syslog_forward_total counter",
			fmt.Sprintf("syslog_forward_total %d", m.forwarded.Load()),
			"# HELP syslog_forward_failed_total Total events that failed to forward.",
			"# TYPE syslog_forward_failed_total counter",
			fmt.Sprintf("syslog_forward_failed_total %d", m.failed.Load()),
			"# HELP syslog_dropped_total Total events dropped (queue full).",
			"# TYPE syslog_dropped_total counter",
			fmt.Sprintf("syslog_dropped_total %d", m.dropped.Load()),
			"# HELP queue_depth Current forwarding queue depth.",
			"# TYPE queue_depth gauge",
			fmt.Sprintf("queue_depth %d", depth),
			"# HELP syslogd_start_time_seconds Unix time when the daemon started.",
			"# TYPE syslogd_start_time_seconds gauge",
			fmt.Sprintf("syslogd_start_time_seconds %d", m.startTime.Unix()),
			"# HELP syslog_throughput_5m Average received syslog msgs/sec over the last 5 minutes.",
			"# TYPE syslog_throughput_5m gauge",
			fmt.Sprintf("syslog_throughput_5m %f", m.through5m()),
		}
		for _, l := range lines {
			fmt.Fprintln(w, l)
		}
	})
}
