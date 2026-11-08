package udp

import (
	"errors"
	"strings"
)

// message is one datagram of the printer. The Buddy firmware sends an RFC 5424
// syslog header, then the points:
//
//	<14>1 - MAC buddy - - - msg=ID,tm=TIME,v=4 POINT\nPOINT\n...
//
// The timestamp, the process ID, the message ID and the structured data are "-".
type message struct {
	// Hostname is the MAC address of the printer.
	Hostname string
	// Points are the point lines, without empty lines.
	Points []string
}

var errNotSyslog = errors.New("not an RFC 5424 message")

// parseMessage parses the syslog header and splits the body into point lines.
func parseMessage(data string) (message, error) {
	var m message
	// PRI and VERSION: "<14>1 ".
	if len(data) < 4 || data[0] != '<' {
		return m, errNotSyslog
	}
	end := strings.IndexByte(data, '>')
	if end < 2 || end > 4 || !strings.HasPrefix(data[end+1:], "1 ") {
		return m, errNotSyslog
	}
	rest := data[end+3:]

	// TIMESTAMP HOSTNAME APP-NAME PROCID MSGID STRUCTURED-DATA, then MSG.
	var fields [6]string
	for i := range fields {
		f, r, ok := strings.Cut(rest, " ")
		if !ok && i < len(fields)-1 {
			return m, errors.New("syslog header is too short")
		}
		fields[i], rest = f, r
	}
	if fields[5] != "-" {
		return m, errors.New("structured data is not supported")
	}
	if fields[1] == "-" || fields[1] == "" {
		return m, errors.New("syslog message has no hostname")
	}
	m.Hostname = fields[1]

	// The body starts with "msg=ID,tm=TIME,v=4 ", then the first point.
	header, body, ok := strings.Cut(rest, " ")
	if !ok || !strings.HasPrefix(header, "msg=") {
		return m, errors.New("body has no msg= header")
	}
	for line := range strings.SplitSeq(body, "\n") {
		if line != "" {
			m.Points = append(m.Points, line)
		}
	}
	return m, nil
}
