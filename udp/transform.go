package udp

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/rs/zerolog/log"
	"gopkg.in/mcuadros/go-syslog.v2/format"
)

type point struct {
	Measurement string
	Tags        map[string]string
	Fields      map[string]interface{} // Use interface{} to handle different field types
	// Delta is the time of the point in µs after the "tm" time of the message header.
	// HasDelta is false if the line has no time.
	Delta    int64
	HasDelta bool
	// Time is the estimated wall time of the sample. It is zero if not known.
	Time time.Time
}

func process(data format.LogParts, prefix string) {
	mac, ip, err := processIdentifiers(data)
	if err != nil {
		log.Error().Msg(fmt.Sprintf("Error processing identifiers: %v", err))
		return
	}
	received := time.Now()
	address := strings.Split(ip, ":")[0]
	lastPush.WithLabelValues(mac, address).Set(float64(received.Unix())) // Set the last push timestamp

	log.Debug().Msg(fmt.Sprintf("Processing data for printer %s", mac))
	metrics, err := processMessage(data["message"].(string), mac, prefix, ip)
	if err != nil {
		log.Error().Msg(fmt.Sprintf("Error processing message: %v", err))
		return
	}

	points := make([]*point, 0, len(metrics))
	for _, line := range metrics {
		point, err := parseLineProtocol(line)
		if err != nil {
			log.Debug().Msgf("Error parsing line '%s': %v", line, err) // printer sends error with several measurements - tmc_read returns "value_too_long" as well as some raw output data
			continue
		}
		points = append(points, point)
	}
	setSampleTimes(points, received)

	for _, point := range points {
		fwName := strings.TrimPrefix(point.Measurement, prefix)
		if msg, ok := errorMessage(point); ok {
			// The firmware sends "name error=\"...\"" when it cannot record a value.
			pointErrors.WithLabelValues(fwName, mac, address).Inc()
			log.Debug().Msgf("Printer %s: error point for metric %s: %s", mac, fwName, msg)
			continue
		}

		collector.recordPoint(*point, fwName, received)
	}
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
			latest = p.Delta
			found = true
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

// processIdentifiers returns the MAC address and ip from the ingested data
func processIdentifiers(data format.LogParts) (string, string, error) {
	mac, ok := data["hostname"].(string)
	if !ok {
		return "", "", fmt.Errorf("mac is not an string")
	}

	ip, ok := data["client"].(string)
	if !ok {
		return "", "", fmt.Errorf("ip is not an string")
	}

	return mac, ip, nil
}

func processMessage(message string, mac string, prefix string, ip string) ([]string, error) {
	messageSplit := strings.Split(message, "\n")

	if len(messageSplit) == 0 {
		return nil, fmt.Errorf("message is empty")
	}

	firstMessage, err := parseFirstMessage(messageSplit[0])

	if err != nil {
		return nil, fmt.Errorf("error parsing first message: %v", err)
	}

	messageSplit = append(messageSplit[1:], firstMessage)

	for i, line := range messageSplit {
		splitted := strings.Split(line, " ")
		splitted, err = updateMetric(splitted, prefix, mac, ip)
		if err != nil {
			log.Error().Msg("Expected error while adding mac label for metric: " + splitted[0] + " error:" + err.Error())
			continue
		}
		messageSplit[i] = strings.Join(splitted, " ")
	}
	return messageSplit, nil
}

func parseFirstMessage(message string) (string, error) {
	splitted := strings.Split(message, " ")
	if len(splitted) == 0 {
		return "", fmt.Errorf("splitted message is empty")
	}
	firstMsg := splitted[1:]
	return strings.Join(firstMsg, " "), nil
}

func updateMetric(splitted []string, prefix string, mac string, ip string) ([]string, error) {
	if len(splitted) == 0 {
		return nil, fmt.Errorf("splitted message is empty")
	}

	splitted[0] = fmt.Sprintf("%s%s,printer_mac=%s,printer_address=%s", prefix, splitted[0], mac, strings.Split(ip, ":")[0])
	return splitted, nil
}

func newPoint() *point {
	return &point{
		Tags:   make(map[string]string),
		Fields: make(map[string]interface{}),
	}
}

func parseLineProtocol(line string) (*point, error) {
	p := newPoint()

	parts := splitLine(line)
	if len(parts) < 2 || len(parts) > 3 {
		return nil, fmt.Errorf("invalid udp format: %s", line) // this happens when printer sends error message
	}

	measurementTags := parts[0]
	measurementTagParts := strings.Split(measurementTags, ",")
	p.Measurement = measurementTagParts[0]

	for i := 1; i < len(measurementTagParts); i++ {
		tag := measurementTagParts[i]
		tagParts := strings.SplitN(tag, "=", 2)
		if len(tagParts) != 2 {
			return nil, fmt.Errorf("invalid tag format: %s", tag)
		}
		p.Tags[tagParts[0]] = unquote(tagParts[1])
	}

	if len(parts) == 3 {
		if d, err := strconv.ParseInt(parts[2], 10, 64); err == nil {
			p.Delta = d
			p.HasDelta = true
		}
	}

	fieldStr := parts[1]
	fieldParts := splitFields(fieldStr)
	for _, field := range fieldParts {
		kv := strings.SplitN(field, "=", 2)
		if len(kv) != 2 {
			return nil, fmt.Errorf("invalid field format: %s", field)
		}
		key := kv[0]
		val := kv[1]

		// parsing metrics as different data types

		if strings.HasSuffix(val, "i") { // Integer
			if iVal, err := strconv.ParseInt(val[:len(val)-1], 10, 64); err == nil {
				p.Fields[key] = iVal
				continue
			}
		}

		if fVal, err := strconv.ParseFloat(val, 64); err == nil { // float
			p.Fields[key] = fVal
			continue
		}

		// After the float check, so that "1" and "0" stay numbers.
		if bVal, err := strconv.ParseBool(val); err == nil { // boolean
			p.Fields[key] = bVal
			continue
		}

		if len(val) >= 2 && strings.HasPrefix(val, "\"") && strings.HasSuffix(val, "\"") { // string
			p.Fields[key] = unquote(val)
			continue
		}

		// fallback
		p.Fields[key] = val
	}

	return p, nil
}

// splitFields splits the field set at each comma that is not in a quoted string.
func splitFields(s string) []string {
	var r []string
	start := 0
	quoted := false
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '\\':
			i++ // skip the escaped character
		case '"':
			quoted = !quoted
		case ',':
			if !quoted {
				r = append(r, s[start:i])
				start = i + 1
			}
		}
	}
	return append(r, s[start:])
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

func splitLine(s string) []string {
	r := []string{}

	// Run a simple finite state machine to handle quoted strings and escaped characters.
	type State int
	const (
		Normal = iota
		QuotedString
	)

	start := 0
	state := Normal
	for i := 0; i < len(s); i++ {
		switch state {
		case Normal:
			switch s[i] {
			case ' ':
				if start < i {
					r = append(r, s[start:i])
				}
				start = i + 1

			case '\\':
				i++ // just skip the next character

			case '"':
				state = QuotedString
			}

		case QuotedString:
			switch s[i] {
			case '\\':
				i++ // just skip the next character

			case '"':
				state = Normal
			}
		}
	}
	if start < len(s) {
		r = append(r, s[start:])
	}

	return r
}
