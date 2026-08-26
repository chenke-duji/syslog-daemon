// Package config loads and validates syslog-daemon configuration from a YAML
// file with optional environment-variable overrides.
package config

import (
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"

	"syslog-daemon/internal/forward"
	"syslog-daemon/internal/syslog"
)

// SyslogConfig holds the UDP syslog listener settings.
type SyslogConfig struct {
	ListenAddr     string `yaml:"listenAddr"`     // e.g. "0.0.0.0:514"
	Workers        int    `yaml:"workers"`        // concurrent processing workers
	ReadBuffer     int    `yaml:"readBufferBytes"` // UDP socket receive buffer
	MaxMessage     int    `yaml:"maxMessageBytes"` // max syslog message size
}

// CEPEngineConfig describes the downstream cep-engine REST endpoint.
type CEPEngineConfig struct {
	BaseURL   string `yaml:"baseUrl"`
	BatchPath string `yaml:"batchPath"`
	SinglePath string `yaml:"singlePath"`
	AuthToken string `yaml:"authToken"`
	Timeout   int    `yaml:"timeoutMs"`
	RetryMax  int    `yaml:"retryMax"`
	RetryBase int    `yaml:"retryBaseMs"`
}

// LoggingConfig controls log level and output file rotation settings.
type LoggingConfig struct {
	Level     string `yaml:"level"` // debug | info | warn | error
	File      string `yaml:"file"`  // empty -> stdout
	MaxSizeMB int    `yaml:"maxSizeMB"`
	MaxBackups int   `yaml:"maxBackups"`
}

// MetricsConfig controls the Prometheus self-monitoring endpoint.
type MetricsConfig struct {
	Enabled    bool   `yaml:"enabled"`
	ListenAddr string `yaml:"listenAddr"`
	Path       string `yaml:"path"`
}

// Config is the root configuration.
type Config struct {
	Syslog    SyslogConfig        `yaml:"syslog"`
	CEPEngine CEPEngineConfig     `yaml:"cepEngine"`
	Forward   forward.ForwardConfig `yaml:"forward"`
	Logging   LoggingConfig       `yaml:"logging"`
	Metrics   MetricsConfig       `yaml:"metrics"`
}

// Load reads a YAML config file and applies defaults and env overrides.
func Load(path string) (*Config, error) {
	cfg := defaultConfig()
	if path != "" {
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("config: read %s: %w", path, err)
		}
		if err := yaml.Unmarshal(data, cfg); err != nil {
			return nil, fmt.Errorf("config: parse %s: %w", path, err)
		}
	}
	applyEnv(cfg)
	if err := validate(cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}

// defaultConfig returns a Config populated with sensible defaults.
func defaultConfig() *Config {
	syslogDefault := syslog.DefaultConfig()
	return &Config{
		Syslog: SyslogConfig{
			ListenAddr: syslogDefault.ListenAddr,
			Workers:    syslogDefault.Workers,
			ReadBuffer: syslogDefault.ReadBuffer,
			MaxMessage: syslogDefault.MaxMessage,
		},
		CEPEngine: CEPEngineConfig{
			BatchPath:  "/api/v1/events/batch",
			SinglePath: "/api/v1/events",
			Timeout:    5000,
			RetryMax:   3,
			RetryBase:  200,
		},
		Forward: forward.DefaultForwardConfig,
		Logging: LoggingConfig{
			Level:      "info",
			MaxSizeMB:  100,
			MaxBackups: 5,
		},
		Metrics: MetricsConfig{
			ListenAddr: ":9092",
			Path:       "/metrics",
		},
	}
}

// applyEnv applies simple SYSD_* environment overrides. Format:
//
//	SYSD_SYSLOG_LISTENADDR, SYSD_CEPENGINE_BASEURL, SYSD_LOGGING_LEVEL,
//	SYSD_METRICS_ENABLED, ...
func applyEnv(cfg *Config) {
	setStr := func(env string, dst *string) {
		if v := os.Getenv(env); v != "" {
			*dst = v
		}
	}
	setStr("SYSD_SYSLOG_LISTENADDR", &cfg.Syslog.ListenAddr)
	setStr("SYSD_CEPENGINE_BASEURL", &cfg.CEPEngine.BaseURL)
	setStr("SYSD_CEPENGINE_AUTHTOKEN", &cfg.CEPEngine.AuthToken)
	setStr("SYSD_LOGGING_LEVEL", &cfg.Logging.Level)
	setStr("SYSD_LOGGING_FILE", &cfg.Logging.File)
	setStr("SYSD_METRICS_LISTENADDR", &cfg.Metrics.ListenAddr)
	setStr("SYSD_METRICS_PATH", &cfg.Metrics.Path)
	if v := os.Getenv("SYSD_METRICS_ENABLED"); v != "" {
		cfg.Metrics.Enabled = strings.EqualFold(v, "true") || v == "1"
	}
}

// validate checks required settings and normalizes values.
func validate(cfg *Config) error {
	if cfg.Syslog.ListenAddr == "" {
		return fmt.Errorf("config: syslog.listenAddr is required")
	}
	if cfg.Syslog.Workers <= 0 {
		cfg.Syslog.Workers = syslog.DefaultConfig().Workers
	}
	if cfg.Syslog.MaxMessage <= 0 {
		cfg.Syslog.MaxMessage = syslog.DefaultConfig().MaxMessage
	}
	if cfg.CEPEngine.BaseURL == "" {
		return fmt.Errorf("config: cepEngine.baseUrl is required")
	}
	if cfg.Forward.QueueFullPolicy == "" {
		cfg.Forward.QueueFullPolicy = string(forward.PolicyDrop)
	}
	switch forward.QueueFullPolicy(cfg.Forward.QueueFullPolicy) {
	case forward.PolicyDrop, forward.PolicyBlock, forward.PolicySingle:
	default:
		return fmt.Errorf("config: unsupported forward.queueFullPolicy %q", cfg.Forward.QueueFullPolicy)
	}
	return nil
}
