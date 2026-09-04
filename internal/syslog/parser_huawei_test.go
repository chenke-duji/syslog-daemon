package syslog

import (
	"testing"
	"time"
)

func TestParseRFC3164WithYear(t *testing.T) {
	// Huawei/Cisco variant: "Mmm dd YYYY HH:MM:SS HOSTNAME ..."
	raw := "<189>Sep  4 2026 08:49:31 CNGUZYUJ2002E %%01OPS/5/OPS_RESTCONF_REQ(s): test"

	msg, err := Parse(raw)
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	// Timestamp must be parsed (not zero)
	if msg.Timestamp.IsZero() {
		t.Fatal("Timestamp should be set for Huawei year format")
	}

	// Verify it's September 4, 2026, 08:49:31
	expected := time.Date(2026, time.September, 4, 8, 49, 31, 0, time.Now().Location())
	if !msg.Timestamp.Equal(expected) {
		t.Errorf("Timestamp = %v, want %v", msg.Timestamp, expected)
	}

	// Hostname should be the device name, not "Sep"
	if msg.Hostname != "CNGUZYUJ2002E" {
		t.Errorf("Hostname = %q, want %q", msg.Hostname, "CNGUZYUJ2002E")
	}

	// Facility and severity from PRI 189 = facility 23 (local7), severity 5 (notice)
	if msg.FacilityValue() != 23 {
		t.Errorf("Facility = %d, want 23", msg.FacilityValue())
	}
	if msg.SeverityValue() != 5 {
		t.Errorf("Severity = %d, want 5", msg.SeverityValue())
	}

	t.Logf("OK: Timestamp=%v Hostname=%q Facility=%d Severity=%d",
		msg.Timestamp, msg.Hostname, msg.FacilityValue(), msg.SeverityValue())
}

func TestParseRFC3164StandardNoYear(t *testing.T) {
	// Standard RFC3164: "Mmm dd HH:MM:SS HOSTNAME ..." (no year)
	raw := "<189>Sep  4 08:49:31 myhost sshd[1234]: test message"

	msg, err := Parse(raw)
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	if msg.Timestamp.IsZero() {
		t.Fatal("Timestamp should be set for standard RFC3164")
	}

	// Hostname should be "myhost", not "Sep"
	if msg.Hostname != "myhost" {
		t.Errorf("Hostname = %q, want %q", msg.Hostname, "myhost")
	}

	t.Logf("OK: Timestamp=%v Hostname=%q", msg.Timestamp, msg.Hostname)
}
