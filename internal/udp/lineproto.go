package udp

import (
	"errors"
	"strconv"
	"strings"
	"sync"
	"time"
)

type valueKind uint8

const (
	valueFloat valueKind = iota
	valueInt
	valueBool
	valueString
)

type tag struct{ key, value string }

// field is one field of a point. num has the value of a number or a bool (1 or 0).
type field struct {
	key  string
	kind valueKind
	num  float64
	str  string
}

// point is one line of the printer, in the InfluxDB line protocol of the firmware:
//
//	name[,tag=value...] field=value[,field=value...] [delta]
//
// The strings refer to the datagram. Copy a string before the collector keeps it.
type point struct {
	Name   string
	Tags   []tag
	Fields []field
	// Delta is the time of the point in µs after the "tm" time of the message header.
	// HasDelta is false if the line has no time.
	Delta    int64
	HasDelta bool
	// Time is the estimated wall time of the sample. It is zero if not known.
	Time time.Time
}

// scratch holds the points of one datagram. The tags and fields of the points are
// parts of the tags and fields slices. scratchPool keeps the slices for the next
// datagram, so a datagram needs few allocations.
type scratch struct {
	points []point
	tags   []tag
	fields []field
}

var scratchPool = sync.Pool{New: func() any { return &scratch{} }}

func (s *scratch) reset() {
	clear(s.points)
	s.points = s.points[:0]
	clear(s.tags)
	s.tags = s.tags[:0]
	clear(s.fields)
	s.fields = s.fields[:0]
}

// parsePoint parses one line into a point. The tags and fields go into s.
func (s *scratch) parsePoint(line string) (point, error) {
	tagStart, fieldStart := len(s.tags), len(s.fields)
	p, err := s.parse(line)
	if err != nil {
		s.tags, s.fields = s.tags[:tagStart], s.fields[:fieldStart]
		return point{}, err
	}
	p.Tags = s.tags[tagStart:len(s.tags):len(s.tags)]
	p.Fields = s.fields[fieldStart:len(s.fields):len(s.fields)]
	return p, nil
}

var (
	errParts        = errors.New("line does not have 2 or 3 parts")
	errNoName       = errors.New("line has no name")
	errInvalidTag   = errors.New("invalid tag")
	errInvalidField = errors.New("invalid field")
)

func (s *scratch) parse(line string) (point, error) {
	var p point
	nameTags, rest := nextUnquoted(line, ' ')
	fields, rest := nextUnquoted(rest, ' ')
	timeStr, rest := nextUnquoted(rest, ' ')
	if nameTags == "" || fields == "" || rest != "" {
		return p, errParts
	}

	p.Name, nameTags = nextUnquoted(nameTags, ',')
	if p.Name == "" {
		return p, errNoName
	}
	for nameTags != "" {
		var t string
		t, nameTags = nextUnquoted(nameTags, ',')
		k, v, ok := strings.Cut(t, "=")
		if !ok || k == "" {
			return p, errInvalidTag
		}
		s.tags = append(s.tags, tag{k, unquote(v)})
	}

	for fields != "" {
		var f string
		f, fields = nextUnquoted(fields, ',')
		k, v, ok := strings.Cut(f, "=")
		if !ok || k == "" {
			return p, errInvalidField
		}
		s.fields = append(s.fields, parseField(k, v))
	}

	if timeStr != "" {
		if d, err := strconv.ParseInt(timeStr, 10, 64); err == nil {
			p.Delta, p.HasDelta = d, true
		}
	}
	return p, nil
}

// parseField returns the field with the value as an integer ("5i"), a float, a bool
// ("T") or a string.
func parseField(k, v string) field {
	if n, ok := strings.CutSuffix(v, "i"); ok {
		if i, err := strconv.ParseInt(n, 10, 64); err == nil {
			return field{key: k, kind: valueInt, num: float64(i)}
		}
	}
	if f, err := strconv.ParseFloat(v, 64); err == nil {
		return field{key: k, kind: valueFloat, num: f}
	}
	// After the float, so that "1" and "0" stay numbers.
	if b, err := strconv.ParseBool(v); err == nil {
		f := field{key: k, kind: valueBool}
		if b {
			f.num = 1
		}
		return f
	}
	return field{key: k, kind: valueString, str: unquote(v)}
}

// nextUnquoted returns the part of s before the first sep that is not in a quoted
// string and not escaped with a backslash, and the rest after sep. For ' ', it skips
// the separators at the start, so that more spaces count as one.
func nextUnquoted(s string, sep byte) (part, rest string) {
	if sep == ' ' {
		s = strings.TrimLeft(s, " ")
	}
	quoted := false
	for i := 0; i < len(s); i++ {
		switch c := s[i]; {
		case c == '\\':
			i++ // skip the escaped character
		case c == '"':
			quoted = !quoted
		case c == sep && !quoted:
			return s[:i], s[i+1:]
		}
	}
	return s, ""
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
func setSampleTimes(points []point, received time.Time) {
	var latest int64
	found := false
	for i := range points {
		if p := &points[i]; p.HasDelta && (!found || p.Delta > latest) {
			latest, found = p.Delta, true
		}
	}
	for i := range points {
		if p := &points[i]; p.HasDelta {
			p.Time = received.Add(-time.Duration(latest-p.Delta) * time.Microsecond)
		}
	}
}

// errorMessage returns the message of an error point. An error point has only the
// field "error" with a string value.
func errorMessage(p *point) (string, bool) {
	if len(p.Fields) != 1 || p.Fields[0].key != "error" || p.Fields[0].kind != valueString {
		return "", false
	}
	return p.Fields[0].str, true
}
