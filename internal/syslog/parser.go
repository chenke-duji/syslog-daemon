package syslog

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// maxHostnameLen is the maximum length of a hostname extracted by the
// vendor-lenient parser. RFC 5424 does not strictly define a hostname length
// limit, but in practice 255 characters is the DNS limit (RFC 1035). We use
// 256 to allow one extra character margin.
const maxHostnameLen = 256

// Parse decodes a raw syslog line into a Message. It tries RFC5424 first, then
// RFC3164, then a lenient vendor-style fallback. A Message is always returned
// (never nil); Raw is always set. Parse errors are returned separately so the
// caller can still forward the raw text if desired.
func Parse(line string) (*Message, error) {
	// Strip trailing CR/LF (single trailing newline only).
	raw := strings.TrimRight(line, "\r\n")

	msg := &Message{Raw: raw}

	// --- Parse <PRI> header ---
	rest, fac, sev, err := parsePriority(raw)
	if err != nil {
		// No PRI: try lenient vendor parsing on the whole line.
		return parseVendorLenient(raw, msg), nil //nolint:nilerr // intentional fallback to lenient parsing
	}
	facCopy, sevCopy := fac, sev
	msg.Facility = &facCopy
	msg.Severity = &sevCopy

	// --- RFC5424 detection ---
	// After PRI, RFC5424 has "VERSION " where VERSION is a single digit.
	if len(rest) > 1 && rest[0] >= '1' && rest[0] <= '9' && (len(rest) == 1 || rest[1] == ' ') {
		if parsed, err := parseRFC5424(rest, msg); err == nil {
			return parsed, nil
		}
		// Fall through to RFC3164 / vendor on failure.
	}

	// --- RFC3164 ---
	if parsed, err := parseRFC3164(rest, msg); err == nil {
		return parsed, nil
	}

	// --- Vendor lenient fallback ---
	return parseVendorLenient(rest, msg), nil
}

// parsePriority extracts "<PRI>" and returns the remaining text.
// Handles both "<PRI>" and the non-standard "<PRI>:" separator.
func parsePriority(line string) (string, int, int, error) {
	if len(line) < 4 || line[0] != '<' {
		return "", 0, 0, fmt.Errorf("no priority header")
	}
	end := strings.IndexByte(line, '>')
	if end < 0 || end > 5 {
		return "", 0, 0, fmt.Errorf("malformed priority")
	}
	priStr := line[1:end]
	pri, err := strconv.Atoi(priStr)
	if err != nil {
		return "", 0, 0, fmt.Errorf("invalid priority: %w", err)
	}
	rest := line[end+1:]
	// Some devices emit "<PRI>:"; strip the stray colon.
	rest = strings.TrimPrefix(rest, ":")
	// Some devices separate PRI with a space; skip a single leading space.
	rest = strings.TrimLeft(rest, " ")
	return rest, pri / 8, pri % 8, nil
}

// parseRFC5424 parses:
//
//	VERSION TIMESTAMP HOSTNAME APP-NAME PROCID MSGID STRUCTURED-DATA [MSG]
func parseRFC5424(s string, msg *Message) (*Message, error) {
	fields := strings.SplitN(s, " ", 7)
	if len(fields) < 6 {
		return nil, fmt.Errorf("rfc5424: too few fields")
	}
	version, err := strconv.Atoi(fields[0])
	if err != nil {
		return nil, fmt.Errorf("rfc5424: bad version")
	}
	msg.Version = version
	msg.Timestamp = parseRFC3339(fields[1])
	msg.Hostname = fields[2]
	msg.AppName = fields[3]
	msg.ProcID = fields[4]
	msg.MsgID = fields[5]

	// STRUCTURED-DATA is fields[6] (may be "-" or include SD elements). The MSG
	// follows, possibly on subsequent text.
	var rest string
	if len(fields) == 7 {
		rest = fields[6]
	}
	msg.StructuredData, rest = parseStructuredData(rest)

	// Everything after SD is the message.
	msg.Message = strings.TrimLeft(rest, " ")
	if msg.Message == "" || msg.Message == "-" {
		msg.Message = ""
	}
	return msg, nil
}

// parseRFC3164 parses:
//
//	Mmm dd hh:mm:ss hostname tag[pid]: message
func parseRFC3164(s string, msg *Message) (*Message, error) {
	// Timestamp: "Jan _2 15:04:05" (RFC3164 month + day + time).
	if len(s) < 15 {
		return nil, fmt.Errorf("rfc3164: too short")
	}
	ts, rest, err := parseRFC3164Timestamp(s)
	if err != nil {
		return nil, err
	}
	msg.Timestamp = ts

	// Hostname: up to next space.
	hostname, rest := nextWord(rest)
	if hostname == "" {
		// Some devices omit hostname; treat rest as message.
		msg.Hostname = ""
		msg.Message = strings.TrimLeft(rest, " ")
		return msg, nil
	}
	msg.Hostname = hostname

	// tag[pid]: message  (tag may contain ':')
	if rest == "" {
		return msg, nil
	}
	// Split tag[pid] from message.
	tag, tagPID, message := parseTagAndMessage(rest)
	msg.Tag = tag
	msg.TagPID = tagPID
	msg.Message = message
	return msg, nil
}

// parseVendorLenient is a best-effort parser for vendor log lines that do not
// conform to RFC3164/RFC5424. It keeps PRI-derived facility/severity (already
// set) and tries to extract a leading hostname and the message text.
func parseVendorLenient(s string, msg *Message) *Message {
	rest := strings.TrimLeft(s, " ")

	// Huawei/Cisco style: "<PRI>YYYY-MM-DD HH:MM:SS HOSTNAME %%module/...: msg".
	// Skip a leading "YYYY-MM-DD HH:MM:SS" timestamp (or a bare ISO8601 date).
	if len(rest) >= 10 && isDigits(rest[:4]) && rest[4] == '-' {
		fields := strings.SplitN(rest, " ", 3)
		// fields[0] looks like a date "YYYY-MM-DD": fields[1]=time, host starts at fields[2].
		if len(fields) >= 2 && len(fields[0]) >= 10 && fields[0][4] == '-' && fields[0][7] == '-' {
			if len(fields) == 3 {
				rest = fields[2]
			}
		}
	}

	// Try "hostname: message" or "hostname message" heuristics.
	if idx := strings.Index(rest, ": "); idx > 0 && idx < maxHostnameLen {
		msg.Hostname = rest[:idx]
		msg.Message = rest[idx+2:]
		return msg
	}
	// Fallback: first whitespace-delimited word as hostname.
	if idx := strings.IndexByte(rest, ' '); idx > 0 && idx < maxHostnameLen {
		msg.Hostname = rest[:idx]
		msg.Message = rest[idx+1:]
		return msg
	}
	msg.Message = rest
	return msg
}

// isDigits reports whether all characters in s are ASCII digits.
func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// parseStructuredData consumes RFC5424 STRUCTURED-DATA. Returns the SD map and
// the remaining text after the last SD element.
//
// This implementation correctly handles escaped characters (\" \\ \]) and
// ']' characters that appear inside quoted parameter values, which the
// previous naive IndexByte(']') approach would misinterpret as element
// terminators.
func parseStructuredData(s string) (map[string]map[string]string, string) {
	sd := make(map[string]map[string]string)
	rest := strings.TrimLeft(s, " ")
	// RFC5424: "-" is the no-structured-data marker. Consume it and its
	// following separator so the rest is the MSG.
	if strings.HasPrefix(rest, "-") {
		rest = strings.TrimLeft(rest[1:], " ")
		return nil, rest
	}
	for {
		rest = strings.TrimLeft(rest, " ")
		if rest == "" {
			break
		}
		if rest[0] != '[' {
			break
		}
		end := findSDElementEnd(rest)
		if end < 0 {
			break
		}
		element := rest[1:end]
		rest = rest[end+1:]
		parseSDElement(element, sd)
	}
	if len(sd) == 0 {
		return nil, rest
	}
	return sd, rest
}

// findSDElementEnd finds the index of the closing ']' for an SD element,
// skipping ']' characters that appear inside quoted parameter values.
func findSDElementEnd(s string) int {
	if s == "" || s[0] != '[' {
		return -1
	}
	inQuote := false
	escaped := false
	for i := 1; i < len(s); i++ {
		if escaped {
			escaped = false
			continue
		}
		c := s[i]
		if c == '\\' && inQuote {
			escaped = true
			continue
		}
		if c == '"' {
			inQuote = !inQuote
			continue
		}
		if c == ']' && !inQuote {
			return i
		}
	}
	return -1
}

// parseSDElement parses one "[sdid k1=v1 k2=v2]" element into sd.
// Handles escaped characters (\" \\ \]) and spaces inside quoted values.
func parseSDElement(element string, sd map[string]map[string]string) {
	// Parse SD-ID (up to first space).
	idx := strings.IndexByte(element, ' ')
	var sdID string
	var rest string
	if idx >= 0 {
		sdID = element[:idx]
		rest = element[idx+1:]
	} else {
		sdID = element
		rest = ""
	}
	if sdID == "" {
		return
	}
	params := make(map[string]string)
	// Parse parameters: key="value" pairs, where value may contain
	// escaped characters and spaces inside quotes.
	for rest != "" {
		rest = strings.TrimLeft(rest, " ")
		if rest == "" {
			break
		}
		eq := strings.IndexByte(rest, '=')
		if eq <= 0 {
			break
		}
		key := rest[:eq]
		rest = rest[eq+1:]
		if rest == "" || rest[0] != '"' {
			// Value is not quoted; take up to next space.
			sp := strings.IndexByte(rest, ' ')
			if sp >= 0 {
				params[key] = rest[:sp]
				rest = rest[sp+1:]
			} else {
				params[key] = rest
				rest = ""
			}
			continue
		}
		// Quoted value: find closing quote, respecting escape sequences.
		rest = rest[1:] // skip opening quote
		var val strings.Builder
		i := 0
		for i < len(rest) {
			c := rest[i]
			if c == '\\' && i+1 < len(rest) {
				// Escape sequence: take next char literally.
				val.WriteByte(rest[i+1])
				i += 2
				continue
			}
			if c == '"' {
				break // closing quote
			}
			val.WriteByte(c)
			i++
		}
		params[key] = val.String()
		if i < len(rest) {
			rest = rest[i+1:] // skip closing quote
		} else {
			rest = ""
		}
	}
	sd[sdID] = params
}

// parseTagAndMessage splits "tag[pid]: message" into tag, pid, message.
//
// To handle tags containing ": " (common in vendor devices that violate RFC
// strictness), this function tries the "]: " separator (tag[pid]: message)
// first, which is unambiguous. If no bracket pattern is found, it searches
// for ": " positions and prefers one where the preceding text (the tag) has
// no space — i.e. it is a single token — falling back to the first ": ".
func parseTagAndMessage(s string) (tag, pid, message string) {
	// Try tag[pid]: message — look for "]: " which reliably separates
	// the tag[pid] portion from the message.
	if bracketIdx := strings.Index(s, "]: "); bracketIdx >= 0 {
		header := s[:bracketIdx+1] // includes ']'
		message = strings.TrimLeft(s[bracketIdx+3:], " ")
		if open := strings.IndexByte(header, '['); open >= 0 {
			tag = strings.TrimSpace(header[:open])
			// Extract pid between '[' and ']'.
			pid = header[open+1 : len(header)-1]
		} else {
			tag = strings.TrimSpace(header)
		}
		return tag, pid, message
	}

	// No [pid] pattern; find ": " that separates tag from message.
	// To handle tags containing ": " (e.g. "my:tag: message"), prefer
	// a ": " position where the preceding text has no space (single-token tag).
	bestIdx := -1
	searchFrom := 0
	for {
		idx := strings.Index(s[searchFrom:], ": ")
		if idx < 0 {
			break
		}
		absIdx := searchFrom + idx
		header := s[:absIdx]
		if !strings.Contains(header, " ") {
			bestIdx = absIdx
			break // preferred: single-token tag
		}
		if bestIdx < 0 {
			bestIdx = absIdx // fallback: first ": "
		}
		searchFrom = absIdx + 2
	}

	if bestIdx >= 0 {
		tag = strings.TrimSpace(s[:bestIdx])
		message = strings.TrimLeft(s[bestIdx+2:], " ")
	} else {
		// No ": " found; entire string is the message.
		message = strings.TrimLeft(s, " ")
	}
	return tag, pid, message
}

// parseRFC3339 parses a strict RFC5424 timestamp. Returns zero time on failure.
func parseRFC3339(s string) time.Time {
	if s == "-" || s == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		// Also accept a space instead of 'T'.
		if idx := strings.IndexByte(s, ' '); idx >= 0 {
			t, err = time.Parse(time.RFC3339, s[:idx]+"T"+s[idx+1:])
		}
		if err != nil {
			return time.Time{}
		}
	}
	return t
}

// parseRFC3164Timestamp parses "Jan _2 15:04:05" (RFC3164 header). Year is
// inferred from the current year. Returns the time and the remaining text.
//
// Year inference: RFC3164 timestamps carry no year. We assume the current
// year; if the resulting date is more than 2 days in the future (allowing for
// timezone differences and minor clock drift), we roll it back to the
// previous year. The 48-hour threshold is more robust than 24h against
// cross-timezone edge cases where "now" in the device's local time may differ
// from the daemon's local time by several hours.
func parseRFC3164Timestamp(s string) (time.Time, string, error) {
	// Month is 3 letters; consume it.
	if len(s) < 15 {
		return time.Time{}, "", fmt.Errorf("rfc3164: short timestamp")
	}
	monthStr := s[0:3]
	rest := strings.TrimLeft(s[3:], " ")
	// Day: 1-2 digits.
	day, rest := nextNum(rest)
	if day == 0 {
		return time.Time{}, "", fmt.Errorf("rfc3164: bad day")
	}
	// Time hh:mm:ss.
	rest = strings.TrimLeft(rest, " ")
	if len(rest) < 8 {
		return time.Time{}, "", fmt.Errorf("rfc3164: short time")
	}
	timeStr := rest[:8]
	rest = strings.TrimLeft(rest[8:], " ")

	now := time.Now()
	month, err := monthNum(monthStr)
	if err != nil {
		return time.Time{}, "", err
	}
	layout := "2006-01-02 15:04:05"
	dateStr := fmt.Sprintf("%04d-%02d-%02d %s", now.Year(), month, day, timeStr)
	t, perr := time.ParseInLocation(layout, dateStr, now.Location())
	if perr != nil {
		return time.Time{}, "", perr
	}
	// If the parsed date is more than 2 days in the future, assume previous year.
	if t.After(now.Add(48 * time.Hour)) {
		t = t.AddDate(-1, 0, 0)
	}
	return t, rest, nil
}

// nextWord consumes a space-delimited word and returns it plus the remainder.
func nextWord(s string) (word, rest string) {
	s = strings.TrimLeft(s, " ")
	if s == "" {
		return "", ""
	}
	idx := strings.IndexByte(s, ' ')
	if idx < 0 {
		return s, ""
	}
	return s[:idx], s[idx+1:]
}

// nextNum consumes a leading 1-2 digit number and returns it plus the remainder.
func nextNum(s string) (int, string) {
	s = strings.TrimLeft(s, " ")
	i := 0
	for i < len(s) && s[i] >= '0' && s[i] <= '9' && i < 2 {
		i++
	}
	if i == 0 {
		return 0, s
	}
	n, _ := strconv.Atoi(s[:i])
	return n, s[i:]
}

// monthNum maps a 3-letter month to its number.
func monthNum(m string) (int, error) {
	months := map[string]int{
		"Jan": 1, "Feb": 2, "Mar": 3, "Apr": 4, "May": 5, "Jun": 6,
		"Jul": 7, "Aug": 8, "Sep": 9, "Oct": 10, "Nov": 11, "Dec": 12,
	}
	if n, ok := months[m]; ok {
		return n, nil
	}
	return 0, fmt.Errorf("rfc3164: bad month %q", m)
}
