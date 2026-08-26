package forward

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"time"

	"syslog-daemon/internal/model"
)

// HTTPConfig holds the cep-engine endpoint settings.
type HTTPConfig struct {
	BaseURL   string `yaml:"baseUrl"`
	BatchPath string `yaml:"batchPath"`
	SinglePath string `yaml:"singlePath"`
	AuthToken string `yaml:"authToken"`
	Timeout   int    `yaml:"timeoutMs"`    // per-request timeout in ms
	RetryMax  int    `yaml:"retryMax"`     // max retries
	RetryBase int    `yaml:"retryBaseMs"`  // exponential backoff base in ms
}

// HTTPForwarder posts RawEvents to cep-engine over REST.
type HTTPForwarder struct {
	baseURL    string
	batchPath  string
	singlePath string
	authToken  string
	client     *http.Client
	retryMax   int
	retryBase  time.Duration
	log        *slog.Logger
}

// NewHTTPForwarder builds an HTTP forwarder from config.
func NewHTTPForwarder(cfg HTTPConfig, log *slog.Logger) (*HTTPForwarder, error) {
	if cfg.BaseURL == "" {
		return nil, fmt.Errorf("forward: cepEngine.baseUrl is required")
	}
	if cfg.BatchPath == "" {
		cfg.BatchPath = "/api/v1/events/batch"
	}
	if cfg.SinglePath == "" {
		cfg.SinglePath = "/api/v1/events"
	}
	timeout := time.Duration(cfg.Timeout) * time.Millisecond
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	retryMax := cfg.RetryMax
	if retryMax < 0 {
		retryMax = 0
	}
	retryBase := time.Duration(cfg.RetryBase) * time.Millisecond
	if retryBase <= 0 {
		retryBase = 200 * time.Millisecond
	}
	return &HTTPForwarder{
		baseURL:    cfg.BaseURL,
		batchPath:  cfg.BatchPath,
		singlePath: cfg.SinglePath,
		authToken:  cfg.AuthToken,
		client:     &http.Client{Timeout: timeout},
		retryMax:   retryMax,
		retryBase:  retryBase,
		log:        log,
	}, nil
}

// ForwardBatch POSTs the events to the batch endpoint with retries.
func (h *HTTPForwarder) ForwardBatch(ctx context.Context, events []model.RawEvent) error {
	if len(events) == 0 {
		return nil
	}
	body, err := json.Marshal(events)
	if err != nil {
		return fmt.Errorf("forward: marshal batch: %w", err)
	}
	url := h.baseURL + h.batchPath
	return h.postWithRetry(ctx, url, body)
}

// ForwardSingle POSTs a single event to the single-event endpoint.
func (h *HTTPForwarder) ForwardSingle(ctx context.Context, event *model.RawEvent) error {
	if event == nil {
		return nil
	}
	body, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("forward: marshal single: %w", err)
	}
	url := h.baseURL + h.singlePath
	return h.postWithRetry(ctx, url, body)
}

// Close is a no-op for the HTTP forwarder (client can be reused).
func (h *HTTPForwarder) Close() error { return nil }

func (h *HTTPForwarder) postWithRetry(ctx context.Context, url string, body []byte) error {
	lastErr := fmt.Errorf("forward: no attempt made")
	for attempt := 0; attempt <= h.retryMax; attempt++ {
		if err := h.postOnce(ctx, url, body); err != nil {
			lastErr = err
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if attempt < h.retryMax {
				wait := h.retryBase * time.Duration(math.Pow(2, float64(attempt)))
				h.log.Warn("forward: POST failed, retrying", "url", url, "attempt", attempt+1, "err", err)
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-time.After(wait):
				}
			}
			continue
		}
		return nil
	}
	return fmt.Errorf("forward: POST %s failed after %d retries: %w", url, h.retryMax, lastErr)
}

func (h *HTTPForwarder) postOnce(ctx context.Context, url string, body []byte) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if h.authToken != "" {
		req.Header.Set("Authorization", "Bearer "+h.authToken)
	}

	resp, err := h.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	// Drain a little for connection reuse.
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("unexpected status %d", resp.StatusCode)
	}
	return nil
}
