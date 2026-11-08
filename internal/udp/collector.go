// Package udp receives the metrics that Prusa printers send over UDP and exposes
// them as Prometheus metrics.
package udp

import (
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

const (
	lastPushName       = "prusa_last_push_timestamp_seconds"
	pointErrorsName    = "prusa_udp_point_errors_total"
	pointErrorsAllName = "prusa_udp_point_errors_all_total"
	pointsName         = "prusa_udp_points_total"
	packetsName        = "prusa_udp_packets_total"
)

var (
	lastPushDesc = prometheus.NewDesc(lastPushName,
		"Time of the last UDP message from the printer.",
		[]string{"printer_mac", "printer_address"}, nil)
	pointErrorsDesc = prometheus.NewDesc(pointErrorsName,
		"Number of error points (error=\"...\") from the printer firmware, by metric. The exporter drops these points.",
		[]string{"metric", "printer_mac", "printer_address"}, nil)
	// The first packet of a printer makes the counters below at 0, so that a dashboard
	// can tell "no points" from "no series".
	pointErrorsAllDesc = prometheus.NewDesc(pointErrorsAllName,
		"Number of error points (error=\"...\") from the printer firmware, all metrics. The exporter drops these points. "+pointErrorsName+" has the count for each metric.",
		[]string{"printer_mac", "printer_address"}, nil)
	pointsDesc = prometheus.NewDesc(pointsName,
		"Number of points from the printer firmware that the exporter parsed, error points and event points included.",
		[]string{"printer_mac", "printer_address"}, nil)
	packetsDesc = prometheus.NewDesc(packetsName,
		"Number of UDP datagrams from the printer that the exporter received.",
		[]string{"printer_mac", "printer_address"}, nil)
)

// reservedNames are the metrics of the collector that are not firmware metrics.
// A firmware metric with one of these names would make each scrape fail.
var reservedNames = map[string]bool{
	lastPushName: true, pointErrorsName: true, pointErrorsAllName: true, pointsName: true, packetsName: true,
}

type metricKind int

const (
	kindGauge metricKind = iota
	// kindCounter counts the event points (v=T) of a metric.
	kindCounter
	// kindInfo is a gauge with the value 1. The label "value" holds a string.
	kindInfo
)

func (k metricKind) valueType() prometheus.ValueType {
	if k == kindCounter {
		return prometheus.CounterValue
	}
	return prometheus.GaugeValue
}

// family is one metric name. All series of a family have the same kind and help text,
// because the registry rejects a scrape that mixes them.
type family struct {
	name string
	help string
	kind metricKind
}

// series is one label set of a family. The label names can be different between the
// series of one family, because the firmware sends a different tag set for some points.
type series struct {
	family      *family
	desc        *prometheus.Desc
	labelValues []string
	value       float64
	// updated is the receive time of the last point. The TTL uses it.
	updated time.Time
	// sampled is the estimated printer time of the last point. It can be zero.
	sampled time.Time
	// info is the string of a kindInfo series.
	info string
	// printerCount is true after the first count point (n=<int>) of an event counter.
	// Then the value is the count of the printer, and v=T points do not change it.
	printerCount bool
}

type printerKey struct{ mac, address string }

// printerStats are the per-printer counters.
type printerStats struct {
	lastPush        time.Time
	packets, points float64
	errors          float64
}

type errorKey struct {
	metric string
	printerKey
}

// Options are the settings of a Collector.
type Options struct {
	// Prefix is the prefix of the metric names, for example "prusa_".
	Prefix string
	// SeriesTTL is the time after which a gauge or info series without a new point
	// goes away. Counters do not expire. Zero or less disables the expiry.
	SeriesTTL time.Duration
	// MaxSeries is the maximum number of series. Zero or less is no limit.
	MaxSeries int
	// SampleTimestamps adds the estimated time of the last point to each sample.
	SampleTimestamps bool
	// EventMetrics are the firmware names (without the prefix) of the event metrics.
	// The first packet of a printer makes the counter of each event at 0. An event
	// that is not in the list gets its counter with its first point.
	EventMetrics []string
}

// Collector keeps the last value of each UDP series and exposes them. It is an
// unchecked prometheus.Collector, because the label names of a metric can change from
// one series to the next.
type Collector struct {
	opts    Options
	logger  *slog.Logger
	metrics *selfMetrics

	mu          sync.Mutex
	families    map[string]*family
	series      map[string]*series
	printers    map[printerKey]*printerStats
	pointErrors map[errorKey]float64
	warned      map[string]bool
	// now returns the current time. Tests replace it.
	now func() time.Time
}

// NewCollector returns a Collector. reg receives the metrics about the UDP receiver.
func NewCollector(opts Options, logger *slog.Logger, reg prometheus.Registerer) *Collector {
	c := &Collector{
		opts:        opts,
		logger:      logger,
		families:    make(map[string]*family),
		series:      make(map[string]*series),
		printers:    make(map[printerKey]*printerStats),
		pointErrors: make(map[errorKey]float64),
		warned:      make(map[string]bool),
		now:         time.Now,
	}
	c.metrics = newSelfMetrics(reg, c.seriesCount)
	return c
}

func (c *Collector) seriesCount() float64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return float64(len(c.series))
}

// Describe sends no descriptors. This makes the collector unchecked.
func (c *Collector) Describe(chan<- *prometheus.Desc) {}

// Collect implements prometheus.Collector. It deletes the expired series first.
func (c *Collector) Collect(ch chan<- prometheus.Metric) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.expire()
	for k, st := range c.printers {
		ch <- prometheus.MustNewConstMetric(lastPushDesc, prometheus.GaugeValue, float64(st.lastPush.UnixNano())/1e9, k.mac, k.address)
		ch <- prometheus.MustNewConstMetric(packetsDesc, prometheus.CounterValue, st.packets, k.mac, k.address)
		ch <- prometheus.MustNewConstMetric(pointsDesc, prometheus.CounterValue, st.points, k.mac, k.address)
		ch <- prometheus.MustNewConstMetric(pointErrorsAllDesc, prometheus.CounterValue, st.errors, k.mac, k.address)
	}
	for k, n := range c.pointErrors {
		ch <- prometheus.MustNewConstMetric(pointErrorsDesc, prometheus.CounterValue, n, k.metric, k.mac, k.address)
	}
	for _, s := range c.series {
		m, err := prometheus.NewConstMetric(s.desc, s.family.kind.valueType(), s.value, s.labelValues...)
		if err != nil {
			c.warnOnce(s.family.name, "Cannot expose a UDP metric", "metric", s.family.name, "err", err)
			continue
		}
		if c.opts.SampleTimestamps && !s.sampled.IsZero() {
			m = prometheus.NewMetricWithTimestamp(s.sampled, m)
		}
		ch <- m
	}
}

// expire deletes the gauge and info series that had no point in the TTL. A counter
// does not expire: if it went away and came back, the reset would hide the count.
// The caller holds c.mu.
func (c *Collector) expire() {
	if c.opts.SeriesTTL <= 0 {
		return
	}
	cutoff := c.now().Add(-c.opts.SeriesTTL)
	for key, s := range c.series {
		if s.family.kind != kindCounter && s.updated.Before(cutoff) {
			delete(c.series, key)
			c.metrics.seriesExpired.Inc()
		}
	}
}

// warnOnce logs a warning only the first time for each key. The caller holds c.mu.
func (c *Collector) warnOnce(key, msg string, args ...any) {
	if c.warned[key] {
		return
	}
	c.warned[key] = true
	c.logger.Warn(msg, args...) //nolint:sloglint // The callers give constant messages.
}

// seriesKey returns a unique key for a metric name and a label set.
func seriesKey(name string, labels map[string]string) string {
	var b strings.Builder
	b.WriteString(name)
	for _, k := range slices.Sorted(maps.Keys(labels)) {
		b.WriteByte(0xff)
		b.WriteString(k)
		b.WriteByte('=')
		b.WriteString(labels[k])
	}
	return b.String()
}

// getSeries returns the series for key. It makes the family and the series if they do
// not exist. It returns nil if the series is not valid (for example if the family has
// a different kind) or if the series limit is reached. The caller holds c.mu.
func (c *Collector) getSeries(key, name string, kind metricKind, help string, labels map[string]string) *series {
	if s, ok := c.series[key]; ok && s.family.kind == kind {
		return s
	}
	if reservedNames[name] {
		c.warnOnce(name, "A UDP metric has a reserved name. The exporter ignores it.", "metric", name)
		return nil
	}
	fam, ok := c.families[name]
	if !ok {
		fam = &family{name: name, help: help, kind: kind}
		c.families[name] = fam
	}
	if fam.kind != kind {
		c.warnOnce(name, "A UDP point needs a different metric type than the first point of the metric. The exporter ignores the point.", "metric", name)
		return nil
	}
	if c.opts.MaxSeries > 0 && len(c.series) >= c.opts.MaxSeries {
		c.warnOnce("\xffmax_series", "The UDP series limit is reached. The exporter drops the points of new series.", "limit", c.opts.MaxSeries)
		return nil
	}

	names := slices.Sorted(maps.Keys(labels))
	values := make([]string, len(names))
	for i, n := range names {
		values[i] = labels[n]
	}
	desc := prometheus.NewDesc(name, fam.help, names, nil)
	if _, err := prometheus.NewConstMetric(desc, kind.valueType(), 0, values...); err != nil {
		c.warnOnce(name, "Cannot make a UDP metric", "metric", name, "labels", names, "err", err)
		return nil
	}
	s := &series{family: fam, desc: desc, labelValues: values}
	c.series[key] = s
	return s
}

// record stores the points of one message. It returns the number of points that it
// stored and the number that it dropped.
func (c *Collector) record(mac, address string, points []*point, received time.Time) (stored, dropped int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	pk := printerKey{mac, address}
	st := c.printer(pk)
	st.lastPush = received
	st.packets++
	st.points += float64(len(points))
	for _, p := range points {
		if msg, ok := errorMessage(p); ok {
			// The firmware sends "name error=\"...\"" when it cannot record a value.
			c.pointErrors[errorKey{p.Name, pk}]++
			st.errors++
			c.metrics.points.WithLabelValues(resultErrorPoint).Inc()
			c.logger.Debug("Error point from the printer", "printer_mac", mac, "metric", p.Name, "error", msg)
			continue
		}
		p.Tags["printer_mac"] = mac
		p.Tags["printer_address"] = address
		if c.recordPoint(p, received) {
			stored++
		} else {
			dropped++
		}
	}
	return stored, dropped
}

// printer returns the stats of the printer. The first time, it makes the counter of
// each known event at 0. The caller holds c.mu.
func (c *Collector) printer(pk printerKey) *printerStats {
	if st, ok := c.printers[pk]; ok {
		return st
	}
	st := &printerStats{}
	c.printers[pk] = st
	labels := map[string]string{"printer_mac": pk.mac, "printer_address": pk.address}
	for _, event := range c.opts.EventMetrics {
		name := c.opts.Prefix + event + "_total"
		c.getSeries(seriesKey(name, labels), name, kindCounter, counterHelp(event, "v"), labels)
	}
	return st
}

// isEvent tells if the metric is an event metric: it is in the list of known events,
// or a v=T point made its counter. The caller holds c.mu.
func (c *Collector) isEvent(fwName string) bool {
	if slices.Contains(c.opts.EventMetrics, fwName) {
		return true
	}
	fam, ok := c.families[c.opts.Prefix+fwName+"_total"]
	return ok && fam.kind == kindCounter
}

// recordPoint stores the fields of one point. It returns false if it dropped a field.
// The caller holds c.mu.
func (c *Collector) recordPoint(p *point, received time.Time) bool {
	ok := true
	for key, value := range p.Fields {
		metricName := c.opts.Prefix + p.Name
		if key != "v" && key != "value" {
			metricName += "_" + key
		}
		if n, isInt := value.(int64); isInt && key == "n" && c.isEvent(p.Name) {
			// The firmware sends the count of events since boot as n=<int>. This value
			// replaces the count of the v=T points. A decrease is a printer restart.
			name := c.opts.Prefix + p.Name + "_total"
			s := c.getSeries(seriesKey(name, p.Tags), name, kindCounter, counterHelp(p.Name, "v"), p.Tags)
			if s == nil {
				ok = false
				continue
			}
			s.value, s.printerCount = float64(n), true
			s.updated, s.sampled = received, p.Time
			continue
		}
		switch v := value.(type) {
		case bool:
			// The firmware sends an event as v=T. Count the events.
			if !v {
				continue
			}
			name := metricName + "_total"
			s := c.getSeries(seriesKey(name, p.Tags), name, kindCounter, counterHelp(p.Name, key), p.Tags)
			if s == nil {
				ok = false
				continue
			}
			if s.printerCount {
				continue
			}
			s.value++
			s.updated, s.sampled = received, p.Time
		case string:
			if !c.recordInfo(metricName+"_info", infoHelp(p.Name, key), p.Tags, v, received, p.Time) {
				ok = false
			}
		default:
			s := c.getSeries(seriesKey(metricName, p.Tags), metricName, kindGauge, gaugeHelp(p.Name, key, value), p.Tags)
			if s == nil {
				ok = false
				continue
			}
			s.value = toFloat64(value)
			s.updated, s.sampled = received, p.Time
		}
	}
	return ok
}

// recordInfo stores a string as the label "value" of an info series with the value 1.
// The tags without the string identify the series. Thus a new string replaces the old
// label value, and each tag set has only one series. The caller holds c.mu.
func (c *Collector) recordInfo(name, help string, tags map[string]string, str string, received, sampled time.Time) bool {
	key := seriesKey(name, tags)
	if old, ok := c.series[key]; ok && old.family.kind == kindInfo && old.info != str {
		delete(c.series, key)
	}
	labels := maps.Clone(tags)
	labels["value"] = str
	s := c.getSeries(key, name, kindInfo, help, labels)
	if s == nil {
		return false
	}
	s.value, s.info = 1, str
	s.updated, s.sampled = received, sampled
	return true
}

func fieldDesc(fwName, field string) string {
	if field == "v" || field == "value" {
		return fmt.Sprintf("UDP metric %q", fwName)
	}
	return fmt.Sprintf("UDP metric %q, field %q,", fwName, field)
}

func infoHelp(fwName, field string) string {
	return "String " + fieldDesc(fwName, field) + " from the printer firmware. The label \"value\" holds the last string. The value is always 1."
}

func counterHelp(fwName, field string) string {
	return "Number of events of " + fieldDesc(fwName, field) + " from the printer firmware. If the firmware sends n=<count>, the value is the count of the printer since boot. Else it is the number of event points since the exporter started."
}

func gaugeHelp(fwName, field string, value any) string {
	kind := "float"
	if _, ok := value.(int64); ok {
		kind = "integer"
	}
	return fieldDesc(fwName, field) + " from the printer firmware, " + kind + " value."
}

func toFloat64(value any) float64 {
	switch v := value.(type) {
	case int64:
		return float64(v)
	case float64:
		return v
	default:
		return 0
	}
}
