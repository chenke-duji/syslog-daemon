// Command syslogd receives syslog messages over UDP and forwards them to a
// cep-engine instance as RawEvents. It uses a bounded, multi-worker batching
// queue with backpressure for high-throughput, and exposes Prometheus metrics.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"syslog-daemon/internal/config"
	"syslog-daemon/internal/forward"
	"syslog-daemon/internal/metrics"
	"syslog-daemon/internal/model"
	"syslog-daemon/internal/syslog"
)

// build info (overridable at link time via -ldflags "-X main.buildVersion=... -X main.buildDate=...").
var (
	buildVersion = "dev"
	buildDate    = "unknown"
)

func main() {
	var configPath string
	var showVersion bool
	flag.StringVar(&configPath, "config", "", "path to YAML config file")
	flag.BoolVar(&showVersion, "v", false, "print build/version info and exit")
	flag.Parse()

	if showVersion {
		fmt.Printf("syslog-daemon %s (%s)\n", buildVersion, buildDate)
		return
	}

	if err := run(configPath); err != nil {
		slog.Error("fatal", "err", err)
		os.Exit(1)
	}
}

// run wires up the daemon and blocks until a termination signal is received.
// It returns an error for any startup failure; graceful shutdown on signal
// returns nil so defers (queue/forwarder/listener cleanup) always run.
func run(configPath string) error {
	cfg, err := config.Load(configPath)
	if err != nil {
		return fmt.Errorf("config load failed: %w", err)
	}

	logger := newLogger(cfg.Logging)

	// --- Forwarder (HTTP to cep-engine) ---
	httpFwd, err := forward.NewHTTPForwarder(&forward.HTTPConfig{
		BaseURL:    cfg.CEPEngine.BaseURL,
		BatchPath:  cfg.CEPEngine.BatchPath,
		SinglePath: cfg.CEPEngine.SinglePath,
		AuthToken:  cfg.CEPEngine.AuthToken,
		Timeout:    cfg.CEPEngine.Timeout,
		RetryMax:   cfg.CEPEngine.RetryMax,
		RetryBase:  cfg.CEPEngine.RetryBase,
	}, logger)
	if err != nil {
		return fmt.Errorf("forwarder init failed: %w", err)
	}
	defer func() { _ = httpFwd.Close() }()

	// --- Batch queue ---
	queue := forward.NewBatchQueue(cfg.Forward, httpFwd, nil, logger)
	queue.Start()
	defer queue.Close()

	// --- Metrics ---
	var metricsSrv *http.Server
	if cfg.Metrics.Enabled {
		m := metrics.New(queue.QueueDepth)
		mux := http.NewServeMux()
		mux.Handle(cfg.Metrics.Path, m.Handler())
		metricsSrv = &http.Server{
			Addr:              cfg.Metrics.ListenAddr,
			Handler:           mux,
			ReadHeaderTimeout: 5 * time.Second, // G112: mitigate Slowloris
		}
		go func() {
			if err := metricsSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				logger.Error("metrics server failed", "err", err)
			}
		}()
		logger.Info("metrics server started", "addr", cfg.Metrics.ListenAddr, "path", cfg.Metrics.Path)
	}

	// --- Syslog listener ---
	handle := func(msg *syslog.Message, sourceIP string) {
		logger.Debug("received syslog",
			"sourceIp", sourceIP,
			"message", msg.Message,
		)
		ev := model.NewFromSyslog(msg, sourceIP, time.Now())
		// Enqueue drops and counts on its own when the queue is full.
		queue.Enqueue(ev)
	}
	listener, err := syslog.NewListener(syslog.Config{
		ListenAddr:   cfg.Syslog.ListenAddr,
		Workers:      cfg.Syslog.Workers,
		ReadBuffer:   cfg.Syslog.ReadBuffer,
		MaxMessage:   cfg.Syslog.MaxMessage,
		AllowedCIDRs: cfg.Syslog.AllowedCIDRs,
	}, handle, logger)
	if err != nil {
		return fmt.Errorf("listener init failed: %w", err)
	}
	if err := listener.Start(); err != nil {
		return fmt.Errorf("listener start failed: %w", err)
	}
	defer listener.Close()

	logger.Info("syslog-daemon started",
		"listen", cfg.Syslog.ListenAddr,
		"workers", cfg.Syslog.Workers,
		"cepEngine", cfg.CEPEngine.BaseURL,
		"queueCapacity", cfg.Forward.QueueCapacity,
	)

	// --- Graceful shutdown ---
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop

	logger.Info("shutting down...")

	if metricsSrv != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		_ = metricsSrv.Shutdown(ctx)
		cancel()
	}
	return nil
}

// newLogger builds a structured logger with optional file rotation.
func newLogger(lc config.LoggingConfig) *slog.Logger {
	var w io.Writer = os.Stdout
	if lc.File != "" {
		w = newRotatingWriter(lc.File, lc.MaxSizeMB, lc.MaxBackups)
	}
	level := parseLevel(lc.Level)
	handler := slog.NewTextHandler(w, &slog.HandlerOptions{Level: level})
	return slog.New(handler)
}

func parseLevel(s string) slog.Level {
	switch strings.ToLower(s) {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// rotatingWriter appends to a file and rolls it over when it exceeds a size
// limit, keeping up to maxBackups rotated files.
type rotatingWriter struct {
	mu         sync.Mutex
	path       string
	maxSize    int64
	maxBackups int
	file       *os.File
}

func newRotatingWriter(path string, maxSizeMB, maxBackups int) *rotatingWriter {
	return &rotatingWriter{
		path:       path,
		maxSize:    int64(maxSizeMB) * 1024 * 1024,
		maxBackups: maxBackups,
	}
}

func (w *rotatingWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	// Ensure file is open.
	if w.file == nil {
		f, err := os.OpenFile(w.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
		if err != nil {
			return 0, err
		}
		w.file = f
	}

	// Rotate if this write would exceed the size limit.
	if info, err := w.file.Stat(); err == nil && info.Size()+int64(len(p)) > w.maxSize {
		w.rotate()
		// rotate closes and nulls w.file; reopen for the new write.
		if w.file == nil {
			f, err := os.OpenFile(w.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
			if err != nil {
				return 0, err
			}
			w.file = f
		}
	}

	return w.file.Write(p)
}

func (w *rotatingWriter) rotate() {
	if w.file != nil {
		_ = w.file.Close()
		w.file = nil
	}
	ts := time.Now().Format("20060102-150405")
	rotated := fmt.Sprintf("%s.%s", w.path, ts)
	_ = os.Rename(w.path, rotated)
	if w.maxBackups > 0 {
		matches, _ := filepath.Glob(w.path + ".*")
		for len(matches) > w.maxBackups {
			oldest := matches[0]
			for _, m := range matches {
				if m < oldest {
					oldest = m
				}
			}
			_ = os.Remove(oldest)
			matches = removeStr(matches, oldest)
		}
	}
}

func removeStr(s []string, v string) []string {
	for i, x := range s {
		if x == v {
			return append(s[:i], s[i+1:]...)
		}
	}
	return s
}
