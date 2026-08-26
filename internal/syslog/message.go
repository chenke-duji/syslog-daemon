// Package syslog decodes RFC3164, RFC5424, and common vendor (Huawei/Cisco)
// syslog message formats into a structured Message.
package syslog

import "time"

// Message is a parsed syslog message. Nil pointer fields indicate the value was
// not present in the message.
type Message struct {
	// Raw is the original line as received (without trailing CR/LF).
	Raw string

	// Facility / Severity (derived from the <PRI> header). Nil if absent.
	Facility *int
	Severity *int

	// Version (RFC5424 only, e.g. 1). 0 if not RFC5424.
	Version int

	// Timestamp of the message. RFC5424 uses a strict RFC3339 time; RFC3164 uses
	// a local-time "Mmm dd hh:mm:ss" header (year inferred from now). Zero if absent.
	Timestamp time.Time

	// Hostname / source host declared in the header.
	Hostname string

	// RFC5424 fields.
	AppName string
	ProcID  string
	MsgID   string

	// RFC3164 legacy tag (e.g. "sshd[123]"). The numeric process id (if any) is
	// split out during parsing.
	Tag    string
	TagPID string

	// StructuredData (RFC5424), keyed by SD-ID. May be empty.
	StructuredData map[string]map[string]string

	// Message is the human-readable text after the header / structured data.
	Message string
}

// FacilityValue returns the facility code, defaulting to -1 when absent.
func (m *Message) FacilityValue() int {
	if m.Facility != nil {
		return *m.Facility
	}
	return -1
}

// SeverityValue returns the severity code, defaulting to -1 when absent.
func (m *Message) SeverityValue() int {
	if m.Severity != nil {
		return *m.Severity
	}
	return -1
}
