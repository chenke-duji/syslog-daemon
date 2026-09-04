package forward

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"syslog-daemon/internal/model"
)

func TestHTTPForwardBatchContract(t *testing.T) {
	var gotPath string
	var gotAuth string
	var gotBody []byte

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		body, _ := io.ReadAll(r.Body)
		gotBody = body
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"accepted"}`))
	}))
	defer srv.Close()

	f, err := NewHTTPForwarder(&HTTPConfig{
		BaseURL:   srv.URL,
		BatchPath: "/api/v1/events/batch",
		AuthToken: "sekret",
		Timeout:   2000,
		RetryMax:  1,
		RetryBase: 10,
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}

	events := []model.RawEvent{
		{
			Source:          model.SourceSyslog,
			SourceIP:        "192.0.2.10",
			ReceivedAt:      1724400000000,
			OriginTimestamp: 1724399999000,
			RawEvent:        "syslog from 192.0.2.10\nfacility: 1 severity: 5\nmessage: test",
			Metadata: map[string]interface{}{
				"facility": 1,
				"severity": 5,
				"hostname": "gw01",
				"tag":      "ospfd",
				"message":  "test",
			},
		},
	}

	if err := f.ForwardBatch(context.Background(), events); err != nil {
		t.Fatalf("forward batch: %v", err)
	}

	if gotPath != "/api/v1/events/batch" {
		t.Fatalf("expected path /api/v1/events/batch, got %s", gotPath)
	}
	if gotAuth != "Bearer sekret" {
		t.Fatalf("expected bearer token, got %q", gotAuth)
	}

	// Verify JSON shape matches cep-engine RawEvent contract.
	var decoded []map[string]any
	if err := json.Unmarshal(gotBody, &decoded); err != nil {
		t.Fatalf("body not valid JSON array: %v", err)
	}
	if len(decoded) != 1 {
		t.Fatalf("expected 1 event in batch, got %d", len(decoded))
	}
	ev := decoded[0]
	for _, key := range []string{"source", "sourceIp", "receivedAt", "originTimestamp", "rawEvent", "metadata"} {
		if _, ok := ev[key]; !ok {
			t.Fatalf("missing required contract field %q", key)
		}
	}
	md, _ := ev["metadata"].(map[string]any)
	if md == nil {
		t.Fatal("metadata missing")
	}
	if md["facility"] == nil {
		t.Fatal("metadata.facility missing")
	}
	if md["severity"] == nil {
		t.Fatal("metadata.severity missing")
	}
	if md["hostname"] != "gw01" {
		t.Fatalf("metadata.hostname = %v, want gw01", md["hostname"])
	}
	if md["message"] != "test" {
		t.Fatalf("metadata.message = %v, want test", md["message"])
	}
}

func TestHTTPForwardRetryAndFail(t *testing.T) {
	attempts := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()

	f, err := NewHTTPForwarder(&HTTPConfig{
		BaseURL:   srv.URL,
		BatchPath: "/api/v1/events/batch",
		Timeout:   1000,
		RetryMax:  2,
		RetryBase: 5,
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}

	events := []model.RawEvent{{Source: "snmp_trap", SourceIP: "192.0.2.10"}}
	if err := f.ForwardBatch(context.Background(), events); err == nil {
		t.Fatal("expected error after retries exhausted")
	}
	if attempts != 3 { // 1 initial + 2 retries
		t.Fatalf("expected 3 attempts (1+2 retries), got %d", attempts)
	}
}
