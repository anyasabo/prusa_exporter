package udp

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// point is one line of the printer, in the InfluxDB line protocol of the firmware:
//
//	name[,tag=value...] field=value[,field=value...] [delta]
type point struct {
	Name   string
	Tags   map[string]string
	Fields map[string]any // int64, float64, bool or string
	// Delta is the time of the point in µs after the "tm" time of the message header.
	// HasDelta is false if the line has no time.
	Delta    int64
	HasDelta bool
	// Time is the estimated wall time of the sample. It is zero if not known.
	Time time.Time
}

func parsePoint(line string) (*point, error) {
	parts := splitUnquoted(line, ' ')
	if len(parts) < 2 || len(parts) > 3 {
		return nil, fmt.Errorf("line has %d parts, not 2 or 3", len(parts))
	}
	p := &point{Tags: map[string]string{}, Fields: map[string]any{}}

	nameTags := splitUnquoted(parts[0], ',')
	p.Name = nameTags[0]
	if p.Name == "" {
		return nil, errors.New("line has no name")
	}
	for _, tag := range nameTags[1:] {
		k, v, ok := strings.Cut(tag, "=")
		if !ok || k == "" {
			return nil, fmt.Errorf("invalid tag %q", tag)
		}
		p.Tags[k] = unquote(v)
	}

	for _, field := range splitUnquoted(parts[1], ',') {
		k, v, ok := strings.Cut(field, "=")
		if !ok || k == "" {
			return nil, fmt.Errorf("invalid field %q", field)
		}
		p.Fields[k] = parseValue(v)
	}

	if len(parts) == 3 {
		if d, err := strconv.ParseInt(parts[2], 10, 64); err == nil {
			p.Delta, p.HasDelta = d, true
		}
	}
	return p, nil
}

// parseValue returns the field value as int64 ("5i"), float64, bool ("T") or string.
func parseValue(v string) any {
	if n, ok := strings.CutSuffix(v, "i"); ok {
		if i, err := strconv.ParseInt(n, 10, 64); err == nil {
			return i
		}
	}
	if f, err := strconv.ParseFloat(v, 64); err == nil {
		return f
	}
	// After the float, so that "1" and "0" stay numbers.
	if b, err := strconv.ParseBool(v); err == nil {
		return b
	}
	return unquote(v)
}

// splitUnquoted splits s at each sep that is not in a quoted string and not escaped
// with a backslash. Empty parts between two separators are removed for ' '.
func splitUnquoted(s string, sep byte) []string {
	var r []string
	start := 0
	quoted := false
	for i := 0; i < len(s); i++ {
		switch c := s[i]; {
		case c == '\\':
			i++ // skip the escaped character
		case c == '"':
			quoted = !quoted
		case c == sep && !quoted:
			if sep != ' ' || start < i {
				r = append(r, s[start:i])
			}
			start = i + 1
		}
	}
	if sep != ' ' || start < len(s) {
		r = append(r, s[start:])
	}
	return r
}

// unquote removes the double quotes around s and the backslash escapes in it.
// The firmware puts quotes around string values and some tag values, and it escapes
// '"' and '\\' with a backslash. If s has no quotes around it, unquote returns s.
func unquote(s string) string {
	if len(s) < 2 || s[0] != '"' || s[len(s)-1] != '"' {
		return s
	}
	s = s[1 : len(s)-1]
	if !strings.Contains(s, "\\") {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+1 < len(s) {
			i++
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// setSampleTimes sets the wall time of each point of one message.
//
// The firmware gives each point a time in µs relative to the first point of the
// message. It sends the message just after it adds the last point. Thus the point
// with the largest time is approximately the receive time, and each other point is
// earlier by the difference.
func setSampleTimes(points []*point, received time.Time) {
	var latest int64
	found := false
	for _, p := range points {
		if p.HasDelta && (!found || p.Delta > latest) {
			latest, found = p.Delta, true
		}
	}
	for _, p := range points {
		if p.HasDelta {
			p.Time = received.Add(-time.Duration(latest-p.Delta) * time.Microsecond)
		}
	}
}

// errorMessage returns the message of an error point. An error point has only the
// field "error" with a string value.
func errorMessage(p *point) (string, bool) {
	if len(p.Fields) != 1 {
		return "", false
	}
	msg, ok := p.Fields["error"].(string)
	return msg, ok
}
