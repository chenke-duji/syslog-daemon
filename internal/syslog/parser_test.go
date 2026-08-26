package syslog

import "testing"

func TestParseRFC3164(t *testing.T) {
	line := "<34>Oct 11 22:14:15 mymachine su: 'su root' failed for lonvick on /dev/pts/8"
	m, err := Parse(line)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if m.FacilityValue() != 4 { // 34/8 = 4
		t.Errorf("facility = %d, want 4", m.FacilityValue())
	}
	if m.SeverityValue() != 2 { // 34%8 = 2
		t.Errorf("severity = %d, want 2", m.SeverityValue())
	}
	if m.Hostname != "mymachine" {
		t.Errorf("hostname = %q, want mymachine", m.Hostname)
	}
	if m.Tag != "su" {
		t.Errorf("tag = %q, want su", m.Tag)
	}
	if m.Message != "'su root' failed for lonvick on /dev/pts/8" {
		t.Errorf("message = %q", m.Message)
	}
	if m.Timestamp.IsZero() {
		t.Error("timestamp should be set")
	}
}

func TestParseRFC3164TagPID(t *testing.T) {
	line := "<30>Oct 11 22:14:15 mymachine sshd[123]: Failed password for root"
	m, err := Parse(line)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if m.Tag != "sshd" {
		t.Errorf("tag = %q, want sshd", m.Tag)
	}
	if m.TagPID != "123" {
		t.Errorf("pid = %q, want 123", m.TagPID)
	}
	if m.Message != "Failed password for root" {
		t.Errorf("message = %q", m.Message)
	}
}

func TestParseRFC5424(t *testing.T) {
	line := `<165>1 2003-10-11T22:14:15.003Z mymachine evntslog - ID47 [exampleSDID@32473 iut="3" eventSource="Application"] An application event log entry`
	m, err := Parse(line)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if m.FacilityValue() != 20 { // 165/8 = 20
		t.Errorf("facility = %d, want 20", m.FacilityValue())
	}
	if m.SeverityValue() != 5 { // 165%8 = 5
		t.Errorf("severity = %d, want 5", m.SeverityValue())
	}
	if m.Version != 1 {
		t.Errorf("version = %d, want 1", m.Version)
	}
	if m.Hostname != "mymachine" {
		t.Errorf("hostname = %q, want mymachine", m.Hostname)
	}
	if m.AppName != "evntslog" {
		t.Errorf("appName = %q, want evntslog", m.AppName)
	}
	if m.MsgID != "ID47" {
		t.Errorf("msgId = %q, want ID47", m.MsgID)
	}
	sd := m.StructuredData["exampleSDID@32473"]
	if sd == nil || sd["iut"] != "3" {
		t.Errorf("structured data iut = %v, want 3", sd)
	}
	if m.Message != "An application event log entry" {
		t.Errorf("message = %q", m.Message)
	}
	if m.Timestamp.IsZero() {
		t.Error("timestamp should be set")
	}
}

func TestParseRFC5424NoSD(t *testing.T) {
	line := "<34>1 2023-08-01T12:00:00Z host01 app - - - hello world"
	m, err := Parse(line)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if m.Hostname != "host01" {
		t.Errorf("hostname = %q, want host01", m.Hostname)
	}
	if m.Message != "hello world" {
		t.Errorf("message = %q", m.Message)
	}
	if m.StructuredData != nil {
		t.Errorf("expected nil structured data, got %v", m.StructuredData)
	}
}

func TestParseVendorHuawei(t *testing.T) {
	// Huawei style: not strict RFC but parseable leniently.
	line := "<134>2023-08-01 12:00:00 huawei-gw01 %%01IFNET/4/LINK_STATE(l)[1]:The interface GigabitEthernet0/0/1 has turned to DOWN"
	m, err := Parse(line)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	// 134/8 = 16 (local0), 134%8 = 6 (info)
	if m.FacilityValue() != 16 {
		t.Errorf("facility = %d, want 16", m.FacilityValue())
	}
	if m.SeverityValue() != 6 {
		t.Errorf("severity = %d, want 6", m.SeverityValue())
	}
	// Lenient: should still have some hostname/message.
	if m.Hostname == "" {
		t.Error("hostname should be extracted")
	}
	if m.Message == "" {
		t.Error("message should be extracted")
	}
}

func TestParseNoPRI(t *testing.T) {
	// Some devices send without PRI.
	line := "Aug 1 12:00:00 host01 kernel: [12345] eth0: link down"
	m, err := Parse(line)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if m.Facility != nil {
		t.Errorf("facility should be nil, got %d", m.FacilityValue())
	}
	if m.Message == "" {
		t.Error("message should be extracted")
	}
}
