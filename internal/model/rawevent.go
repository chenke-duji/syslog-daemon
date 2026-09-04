// Package model defines the RawEvent JSON structure that syslog-daemon builds
// and forwards to cep-engine. Field names and structure are strictly aligned
// with cep-engine's com.dujitech.cep.model.RawEvent (deserialized with Gson,
// so JSON keys must match the Java field names exactly).
package model

import (
	"crypto/sha256"
	"fmt"
	"strings"
	"time"

	"syslog-daemon/internal/syslog"
)

// RawEvent is the payload posted to cep-engine.
type RawEvent struct {
	Source          string                 `json:"source"`
	SourceIP        string                 `json:"sourceIp"`
	ReceivedAt      int64                  `json:"receivedAt"`
	OriginTimestamp int64                  `json:"originTimestamp"`
	RawEvent        string                 `json:"rawEvent"`
	Metadata        map[string]interface{} `json:"metadata"`
}

// Source value used for syslog messages.
const SourceSyslog = "syslog"

// NewFromSyslog builds a RawEvent from a decoded syslog message.
//
// Metadata carries the structured syslog fields consumed by cep-engine's
// syslog_parser.groovy:
//
//	facility (int), facilityLabel (string), severity (int), severityLabel (string),
//	version (int), timestamp (RFC3339), hostname, appName, procId, msgId,
//	tag (legacy tag for RFC3164), structuredData (map[string]map[string]string),
//	message (the actual log text), rawMessage (the full raw line).
//
// OriginTimestamp uses the syslog header timestamp when present (millis, UTC),
// otherwise falls back to a deterministic hash of the raw message so that
// multiple Active-Active daemon instances still produce an identical value for
// the same device event (required by cep-engine transport dedup).
func NewFromSyslog(msg *syslog.Message, sourceIP string, receivedAt time.Time) *RawEvent {
	metadata := make(map[string]interface{}, 14)
	if msg.Facility != nil {
		metadata["facility"] = *msg.Facility
		metadata["facilityLabel"] = facilityLabel(*msg.Facility)
	}
	if msg.Severity != nil {
		metadata["severity"] = *msg.Severity
		metadata["severityLabel"] = severityLabel(*msg.Severity)
	}
	if msg.Version > 0 {
		metadata["version"] = msg.Version
	}
	if !msg.Timestamp.IsZero() {
		metadata["timestamp"] = msg.Timestamp.UTC().Format(time.RFC3339Nano)
	}
	if msg.Hostname != "" {
		metadata["hostname"] = msg.Hostname
	}
	if msg.AppName != "" {
		metadata["appName"] = msg.AppName
	}
	if msg.ProcID != "" {
		metadata["procId"] = msg.ProcID
	}
	if msg.MsgID != "" {
		metadata["msgId"] = msg.MsgID
	}
	if msg.Tag != "" {
		metadata["tag"] = msg.Tag
	}
	if len(msg.StructuredData) > 0 {
		metadata["structuredData"] = msg.StructuredData
	}
	metadata["message"] = msg.Message
	metadata["rawMessage"] = msg.Raw

	rawText := renderRawText(msg, sourceIP)

	origin := msg.Timestamp
	if origin.IsZero() {
		origin = time.UnixMilli(deterministicHash(msg.Raw))
	}

	return &RawEvent{
		Source:          SourceSyslog,
		SourceIP:        sourceIP,
		ReceivedAt:      receivedAt.UnixMilli(),
		OriginTimestamp: origin.UnixMilli(),
		RawEvent:        rawText,
		Metadata:        metadata,
	}
}

// renderRawText produces a stable, human-readable text of the syslog message.
// It is deterministic (no receivedAt) so it can be used in the dedup fingerprint.
func renderRawText(msg *syslog.Message, sourceIP string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "syslog from %s\n", sourceIP)
	fmt.Fprintf(&b, "facility: %d severity: %d\n",
		msg.FacilityValue(), msg.SeverityValue())
	fmt.Fprintf(&b, "timestamp: %s\n", msg.Timestamp.UTC().Format(time.RFC3339Nano))
	if msg.Hostname != "" {
		fmt.Fprintf(&b, "hostname: %s\n", msg.Hostname)
	}
	if msg.AppName != "" {
		fmt.Fprintf(&b, "appName: %s\n", msg.AppName)
	}
	fmt.Fprintf(&b, "message: %s\n", msg.Message)
	return b.String()
}

// deterministicHash returns a stable int64 derived from a string.
// Uses the first 8 bytes of SHA-256 as an unsigned 63-bit value to avoid
// sign-overflow concerns from the left-shift accumulation.
func deterministicHash(s string) int64 {
	sum := sha256.Sum256([]byte(s))
	var u uint64
	for i := 0; i < 8; i++ {
		u = u<<8 | uint64(sum[i])
	}
	// Clear the sign bit so the value fits in int64 without overflow.
	v := int64(u &^ (1 << 63)) //#nosec G115 -- masked to <= 2^63-1, conversion is safe
	if v == 0 {
		v = 1 // ensure non-zero return
	}
	return v
}

// facilityLabel maps a syslog facility code to its RFC3164 label.
func facilityLabel(code int) string {
	if code >= 0 && code < len(facilityLabels) {
		return facilityLabels[code]
	}
	return "unknown"
}

var facilityLabels = [...]string{
	"kern", "user", "mail", "daemon", "auth", "syslog", "lpr", "news",
	"uucp", "cron", "authpriv", "ftp", "ntp", "security", "console", "solaris-cron",
	"local0", "local1", "local2", "local3", "local4", "local5", "local6", "local7",
}

// severityLabel maps a syslog severity code to its RFC3164 label.
func severityLabel(code int) string {
	if code >= 0 && code < len(severityLabels) {
		return severityLabels[code]
	}
	return "unknown"
}

var severityLabels = [...]string{
	"emerg", "alert", "crit", "err", "warning", "notice", "info", "debug",
}
