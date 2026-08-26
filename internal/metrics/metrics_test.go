package metrics

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCountersAndHandler(t *testing.T) {
	depth := 7
	m := New(func() int { return depth })

	m.IncReceived()
	m.IncReceived()
	m.IncForwarded(2)
	m.IncForwardFailed(1)
	m.IncDropped(1)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	m.Handler().ServeHTTP(rec, req)

	body := rec.Body.String()
	want := []string{
		"syslog_received_total 2",
		"syslog_forward_total 2",
		"syslog_forward_failed_total 1",
		"syslog_dropped_total 1",
		"queue_depth 7",
		"syslogd_start_time_seconds",
		"syslog_throughput_5m",
	}
	for _, w := range want {
		if !strings.Contains(body, w) {
			t.Fatalf("metrics output missing %q in:\n%s", w, body)
		}
	}
}

func TestThroughput5m(t *testing.T) {
	m := New(func() int { return 0 })
	// Simulate 100 received events across the window.
	for i := 0; i < 100; i++ {
		m.IncReceived()
	}
	thr := m.through5m()
	// With 300s window, 100 events in a short burst -> average = 100/300.
	if thr <= 0 {
		t.Fatalf("expected positive throughput, got %f", thr)
	}
}
