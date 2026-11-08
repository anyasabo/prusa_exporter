// Package udp receives the metrics that Prusa printers send over UDP and exposes
// them as Prometheus metrics.
package udp

import (
	"context"
	"fmt"
	"log/slog"
	"net/netip"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/anyasabo/prusa_exporter/internal/logging"
)

// The label names that identify a printer on the UDP metrics.
const (
	labelMAC     = "printer_mac"
	labelAddress = "printer_address"
)

var printerLabels = []string{labelMAC, labelAddress}

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
		printerLabels, nil)
	pointErrorsDesc = prometheus.NewDesc(pointErrorsName,
		"Number of error points (error=\"...\") from the printer firmware, by metric. The exporter drops these points.",
		slices.Concat([]string{"metric"}, printerLabels), nil)
	// The first packet of a printer makes the counters below at 0, so that a dashboard
	// can tell "no points" from "no series".
	pointErrorsAllDesc = prometheus.NewDesc(pointErrorsAllName,
		"Number of error points (error=\"...\") from the printer firmware, all metrics. The exporter drops these points. "+pointErrorsName+" has the count for each metric.",
		printerLabels, nil)
	pointsDesc = prometheus.NewDesc(pointsName,
		"Number of points from the printer firmware that the exporter parsed, error points and event points included.",
		printerLabels, nil)
	packetsDesc = prometheus.NewDesc(packetsName,
		"Number of UDP datagrams from the printer that the exporter received.",
		printerLabels, nil)
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

type printerKey struct {
	mac  string
	addr netip.Addr
}

// printerStats are the per-printer counters.
type printerStats struct {
	mac, address    string
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
//
// The maps do not keep strings that refer to a datagram: the collector copies each
// string that it keeps. The map lookups use m[string(buf)], which does not allocate.
type Collector struct {
	opts    Options
	logger  *slog.Logger
	metrics *selfMetrics

	mu          sync.Mutex
	families    map[string]*family
	series      map[string]*series
	printers    map[printerKey]*printerStats
	pointErrors map[errorKey]*float64
	warned      map[string]bool
	// Buffers for the metric name, the series key and the labels of the current point.
	nameBuf, keyBuf []byte
	labels          []tag
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
		pointErrors: make(map[errorKey]*float64),
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
		ch <- prometheus.MustNewConstMetric(lastPushDesc, prometheus.GaugeValue, float64(st.lastPush.UnixNano())/1e9, k.mac, st.address)
		ch <- prometheus.MustNewConstMetric(packetsDesc, prometheus.CounterValue, st.packets, k.mac, st.address)
		ch <- prometheus.MustNewConstMetric(pointsDesc, prometheus.CounterValue, st.points, k.mac, st.address)
		ch <- prometheus.MustNewConstMetric(pointErrorsAllDesc, prometheus.CounterValue, st.errors, k.mac, st.address)
	}
	for k, n := range c.pointErrors {
		ch <- prometheus.MustNewConstMetric(pointErrorsDesc, prometheus.CounterValue, *n, k.metric, k.mac, c.printers[k.printerKey].address)
	}
	for _, s := range c.series {
		m, err := prometheus.NewConstMetric(s.desc, s.family.kind.valueType(), s.value, s.labelValues...)
		if err != nil {
			c.warnOnce(s.family.name, "Cannot expose a UDP metric", logging.MetricName, s.family.name, logging.ErrorMessage, err)
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
	c.warned[strings.Clone(key)] = true
	c.logger.Warn(msg, args...) //nolint:sloglint // The callers give constant messages.
}

// helpSpec has the data for the help text of a family. The collector makes the text
// only for a new family.
type helpSpec struct {
	kind    metricKind
	fwName  string
	field   string
	integer bool
}

func (h helpSpec) text() string {
	desc := fmt.Sprintf("UDP metric %q", h.fwName)
	if h.field != "v" && h.field != "value" {
		desc = fmt.Sprintf("UDP metric %q, field %q,", h.fwName, h.field)
	}
	switch h.kind {
	case kindInfo:
		return "String " + desc + " from the printer firmware. The label \"value\" holds the last string. The value is always 1."
	case kindCounter:
		return "Number of events of " + desc + " from the printer firmware. If the firmware sends n=<count>, the value is the count of the printer since boot. Else it is the number of event points since the exporter started."
	default:
		kind := "float"
		if h.integer {
			kind = "integer"
		}
		return desc + " from the printer firmware, " + kind + " value."
	}
}

// setName writes the metric name to c.nameBuf: the prefix, the firmware name, the
// field (if not "v" or "value") and the suffix. The caller holds c.mu.
func (c *Collector) setName(fwName, fieldKey, suffix string) {
	c.nameBuf = append(c.nameBuf[:0], c.opts.Prefix...)
	c.nameBuf = append(c.nameBuf, fwName...)
	if fieldKey != "v" && fieldKey != "value" && fieldKey != "" {
		c.nameBuf = append(c.nameBuf, '_')
		c.nameBuf = append(c.nameBuf, fieldKey...)
	}
	c.nameBuf = append(c.nameBuf, suffix...)
}

// setKey writes the series key of c.nameBuf and c.labels to c.keyBuf. The caller
// holds c.mu.
func (c *Collector) setKey() {
	c.keyBuf = append(c.keyBuf[:0], c.nameBuf...)
	for _, l := range c.labels {
		c.keyBuf = append(c.keyBuf, 0xff)
		c.keyBuf = append(c.keyBuf, l.key...)
		c.keyBuf = append(c.keyBuf, '=')
		c.keyBuf = append(c.keyBuf, l.value...)
	}
}

// setLabels writes the tags of p and the printer labels to c.labels, sorted by name.
// The printer labels replace a tag with the same name. The caller holds c.mu.
func (c *Collector) setLabels(tags []tag, mac, address string) {
	c.labels = append(c.labels[:0], tag{labelAddress, address}, tag{labelMAC, mac})
	for _, t := range tags {
		if t.key != labelAddress && t.key != labelMAC {
			c.labels = append(c.labels, t)
		}
	}
	slices.SortFunc(c.labels, func(a, b tag) int { return strings.Compare(a.key, b.key) })
}

// getSeries returns the series of c.keyBuf. It makes the family (c.nameBuf) and the
// series (c.labels, and extra if its key is not empty) if they do not exist. It
// returns nil if the series is not valid (for example if the family has a different
// kind) or if the series limit is reached. The caller holds c.mu.
func (c *Collector) getSeries(help helpSpec, extra tag) *series {
	if s, ok := c.series[string(c.keyBuf)]; ok && s.family.kind == help.kind {
		return s
	}
	if reservedNames[string(c.nameBuf)] {
		c.warnOnce(string(c.nameBuf), "A UDP metric has a reserved name. The exporter ignores it.", logging.MetricName, string(c.nameBuf))
		return nil
	}
	fam, ok := c.families[string(c.nameBuf)]
	if !ok {
		name := string(c.nameBuf)
		fam = &family{name: name, help: help.text(), kind: help.kind}
		c.families[name] = fam
	}
	if fam.kind != help.kind {
		c.warnOnce(fam.name, "A UDP point needs a different metric type than the first point of the metric. The exporter ignores the point.", logging.MetricName, fam.name)
		return nil
	}
	if c.opts.MaxSeries > 0 && len(c.series) >= c.opts.MaxSeries {
		c.warnOnce("\xffmax_series", "The UDP series limit is reached. The exporter drops the points of new series.", logging.SeriesLimit, c.opts.MaxSeries)
		return nil
	}

	labels := c.labels
	if extra.key != "" {
		labels = append(slices.Clone(labels), extra)
		slices.SortFunc(labels, func(a, b tag) int { return strings.Compare(a.key, b.key) })
	}
	names := make([]string, len(labels))
	values := make([]string, len(labels))
	for i, l := range labels {
		names[i], values[i] = strings.Clone(l.key), strings.Clone(l.value)
	}
	desc := prometheus.NewDesc(fam.name, fam.help, names, nil)
	if _, err := prometheus.NewConstMetric(desc, help.kind.valueType(), 0, values...); err != nil {
		c.warnOnce(fam.name, "Cannot make a UDP metric", logging.MetricName, fam.name, logging.MetricLabels, names, logging.ErrorMessage, err)
		return nil
	}
	s := &series{family: fam, desc: desc, labelValues: values}
	c.series[string(c.keyBuf)] = s
	return s
}

// record stores the points of one message. It returns the number of points that it
// stored and the number that it dropped.
func (c *Collector) record(ctx context.Context, mac string, addr netip.Addr, points []point, received time.Time) (stored, dropped int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	pk, st := c.printer(printerKey{mac, addr})
	st.lastPush = received
	st.packets++
	st.points += float64(len(points))
	for i := range points {
		p := &points[i]
		if msg, ok := errorMessage(p); ok {
			// The firmware sends "name error=\"...\"" when it cannot record a value.
			ek := errorKey{p.Name, pk}
			n := c.pointErrors[ek]
			if n == nil {
				n = new(float64)
				ek.metric = strings.Clone(ek.metric)
				c.pointErrors[ek] = n
			}
			*n++
			st.errors++
			c.metrics.errorPoints.Inc()
			if c.logger.Enabled(ctx, slog.LevelDebug) {
				c.logger.DebugContext(ctx, "Error point from the printer", logging.PrinterMAC, mac, logging.MetricName, p.Name, logging.PointError, msg)
			}
			continue
		}
		c.setLabels(p.Tags, pk.mac, st.address)
		if c.recordPoint(p, received) {
			stored++
		} else {
			dropped++
		}
	}
	return stored, dropped
}

// printer returns the key in the map and the stats of the printer. The first time, it
// makes the counter of each known event at 0. pk.mac can refer to a datagram. The
// returned key does not. The caller holds c.mu.
func (c *Collector) printer(pk printerKey) (printerKey, *printerStats) {
	if st, ok := c.printers[pk]; ok {
		return printerKey{st.mac, pk.addr}, st
	}
	pk.mac = strings.Clone(pk.mac)
	st := &printerStats{mac: pk.mac, address: pk.addr.String()}
	c.printers[pk] = st
	c.setLabels(nil, pk.mac, st.address)
	for _, event := range c.opts.EventMetrics {
		c.setName(event, "", "_total")
		c.setKey()
		c.getSeries(helpSpec{kind: kindCounter, fwName: event, field: "v"}, tag{})
	}
	return pk, st
}

// isEvent tells if the metric is an event metric: it is in the list of known events,
// or a v=T point made its counter. The caller holds c.mu.
func (c *Collector) isEvent(fwName string) bool {
	if slices.Contains(c.opts.EventMetrics, fwName) {
		return true
	}
	c.setName(fwName, "", "_total")
	fam, ok := c.families[string(c.nameBuf)]
	return ok && fam.kind == kindCounter
}

// recordPoint stores the fields of one point with the labels in c.labels. It returns
// false if it dropped a field. The caller holds c.mu.
func (c *Collector) recordPoint(p *point, received time.Time) bool {
	ok := true
	for _, f := range p.Fields {
		if f.kind == valueInt && f.key == "n" && c.isEvent(p.Name) {
			// The firmware sends the count of events since boot as n=<int>. This value
			// replaces the count of the v=T points. A decrease is a printer restart.
			c.setName(p.Name, "", "_total")
			c.setKey()
			s := c.getSeries(helpSpec{kind: kindCounter, fwName: p.Name, field: "v"}, tag{})
			if s == nil {
				ok = false
				continue
			}
			s.value, s.printerCount = f.num, true
			s.updated, s.sampled = received, p.Time
			continue
		}
		switch f.kind {
		case valueBool:
			// The firmware sends an event as v=T. Count the events.
			if f.num == 0 {
				continue
			}
			c.setName(p.Name, f.key, "_total")
			c.setKey()
			s := c.getSeries(helpSpec{kind: kindCounter, fwName: p.Name, field: f.key}, tag{})
			if s == nil {
				ok = false
				continue
			}
			if s.printerCount {
				continue
			}
			s.value++
			s.updated, s.sampled = received, p.Time
		case valueString:
			if !c.recordInfo(p, f, received) {
				ok = false
			}
		default:
			c.setName(p.Name, f.key, "")
			c.setKey()
			s := c.getSeries(helpSpec{kind: kindGauge, fwName: p.Name, field: f.key, integer: f.kind == valueInt}, tag{})
			if s == nil {
				ok = false
				continue
			}
			s.value = f.num
			s.updated, s.sampled = received, p.Time
		}
	}
	return ok
}

// recordInfo stores a string as the label "value" of an info series with the value 1.
// The tags without the string identify the series. Thus a new string replaces the old
// label value, and each tag set has only one series. The caller holds c.mu.
func (c *Collector) recordInfo(p *point, f field, received time.Time) bool {
	c.setName(p.Name, f.key, "_info")
	c.setKey()
	if old, ok := c.series[string(c.keyBuf)]; ok && old.family.kind == kindInfo && old.info != f.str {
		delete(c.series, string(c.keyBuf))
	}
	s := c.getSeries(helpSpec{kind: kindInfo, fwName: p.Name, field: f.key}, tag{"value", f.str})
	if s == nil {
		return false
	}
	if s.info != f.str {
		s.info = strings.Clone(f.str)
	}
	s.value = 1
	s.updated, s.sampled = received, p.Time
	return true
}
