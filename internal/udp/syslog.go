package udp

import (
	"errors"
	"strings"
)

// parseMessage parses the syslog header of one datagram of the printer. The Buddy
// firmware sends an RFC 5424 header, then the points:
//
//	<14>1 - MAC buddy - - - msg=ID,tm=TIME,v=4 POINT\nPOINT\n...
//
// The timestamp, the process ID, the message ID and the structured data are "-".
// parseMessage returns the hostname (the MAC address of the printer) and the point
// lines. Use nextLine to read the lines.
func parseMessage(data string) (hostname, lines string, err error) {
	// PRI and VERSION: "<14>1 ".
	if len(data) < 4 || data[0] != '<' {
		return "", "", errNotSyslog
	}
	end := strings.IndexByte(data, '>')
	if end < 2 || end > 4 || !strings.HasPrefix(data[end+1:], "1 ") {
		return "", "", errNotSyslog
	}
	rest := data[end+3:]

	// TIMESTAMP HOSTNAME APP-NAME PROCID MSGID STRUCTURED-DATA, then MSG.
	var fields [6]string
	for i := range fields {
		f, r, ok := strings.Cut(rest, " ")
		if !ok && i < len(fields)-1 {
			return "", "", errShortHeader
		}
		fields[i], rest = f, r
	}
	if fields[5] != "-" {
		return "", "", errStructuredData
	}
	if fields[1] == "-" || fields[1] == "" {
		return "", "", errNoHostname
	}

	// The body starts with "msg=ID,tm=TIME,v=4 ", then the first point.
	header, body, ok := strings.Cut(rest, " ")
	if !ok || !strings.HasPrefix(header, "msg=") {
		return "", "", errNoMsgHeader
	}
	return fields[1], body, nil
}

// nextLine returns the first line of s that is not empty, and the rest after it.
// The line is empty if s has no more lines.
func nextLine(s string) (line, rest string) {
	for s != "" {
		line, s, _ = strings.Cut(s, "\n")
		if line != "" {
			return line, s
		}
	}
	return "", ""
}

var (
	errNotSyslog      = errors.New("not an RFC 5424 message")
	errShortHeader    = errors.New("syslog header is too short")
	errStructuredData = errors.New("structured data is not supported")
	errNoHostname     = errors.New("syslog message has no hostname")
	errNoMsgHeader    = errors.New("body has no msg= header")
)
