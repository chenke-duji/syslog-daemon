package forward

import (
	"context"
	"fmt"
	"log/slog"

	"syslog-daemon/internal/model"
)

// KafkaConfig holds optional Kafka forwarding settings (reserved channel).
type KafkaConfig struct {
	Enabled bool   `yaml:"enabled"`
	Brokers []string `yaml:"brokers"`
	Topic   string `yaml:"topic"`
}

// KafkaForwarder is a reserved forwarding channel. The Kafka integration is
// planned as an optional transport; this stub implements the Forwarder
// interface and fails loudly when used, so the wiring compiles today and the
// real client (e.g. segmentio/kafka-go) can be added without changing the
// batch-queue or main assembly.
type KafkaForwarder struct {
	cfg KafkaConfig
	log *slog.Logger
}

// NewKafkaForwarder builds a reserved Kafka forwarder stub.
func NewKafkaForwarder(cfg KafkaConfig, log *slog.Logger) (*KafkaForwarder, error) {
	if cfg.Enabled {
		log.Warn("forward: kafka forwarder is reserved and not yet implemented; HTTP will be used")
	}
	return &KafkaForwarder{cfg: cfg, log: log}, nil
}

// ForwardBatch is not implemented in the reserved stub.
func (k *KafkaForwarder) ForwardBatch(ctx context.Context, events []model.RawEvent) error {
	return fmt.Errorf("forward: kafka forwarder not implemented (reserved)")
}

// ForwardSingle is not implemented in the reserved stub.
func (k *KafkaForwarder) ForwardSingle(ctx context.Context, event *model.RawEvent) error {
	return fmt.Errorf("forward: kafka forwarder not implemented (reserved)")
}

// Close is a no-op for the stub.
func (k *KafkaForwarder) Close() error { return nil }
