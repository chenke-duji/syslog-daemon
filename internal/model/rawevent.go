// Package model defines the RawEvent JSON structure that syslog-daemon builds
// and forwards to cep-engine. Field names and structure are strictly aligned
// with cep-engine's com.dujitech.cep.model.RawEvent (deserialized with Gson,
// so JSON keys must match the Java field names exactly).
package model

import (
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

	// OriginTimestamp: use the parsed syslog timestamp (millis, UTC).
	// When timestamp parsing fails, leave it 0 — NOT a hash. A hash value
	// is not a real timestamp and would produce nonsensical firstOccurrence
	// values in the Groovy parser. Two Active-Active collectors will both
	// send 0, so transport dedup (fingerprint includes originTimestamp)
	// still works; the Groovy parser falls through to System.currentTimeMillis().
	var originTs int64
	if !msg.Timestamp.IsZero() {
		originTs = msg.Timestamp.UnixMilli()
	}

	return &RawEvent{
		Source:          SourceSyslog,
		SourceIP:        sourceIP,
		ReceivedAt:      receivedAt.UnixMilli(),
		OriginTimestamp: originTs,
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
