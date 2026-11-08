package udp

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/rs/zerolog/log"
)

var (
	lastPush = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "prusa_last_push_timestamp",
			Help: "Last time the printer pushed metrics to the exporter.",
		},
		[]string{"printer_mac", "printer_address"},
	)
	udpRegistry *prometheus.Registry

	// collector holds the series made from the UDP points. Init sets it.
	collector *udpCollector
)

// Init initializes the Prometheus udp registry.
// Call Init before MetricsListener.
func Init(udpMainRegistry *prometheus.Registry) {
	udpRegistry = udpMainRegistry
	collector = newUDPCollector()
	lastPush.Reset()

	udpRegistry.MustRegister(lastPush)
	udpRegistry.MustRegister(collector)
}

type metricKind int

const (
	kindGauge metricKind = iota
)

func (k metricKind) valueType() prometheus.ValueType {
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
	updated     time.Time
}

// udpCollector is an unchecked prometheus.Collector. It keeps the last value of each
// series and makes const metrics at each scrape.
type udpCollector struct {
	mu       sync.Mutex
	families map[string]*family
	series   map[string]*series
	warned   map[string]bool
}

func newUDPCollector() *udpCollector {
	return &udpCollector{
		families: make(map[string]*family),
		series:   make(map[string]*series),
		warned:   make(map[string]bool),
	}
}

// Describe sends no descriptors. This makes the collector unchecked, so the label
// names of a metric can change from one series to the next.
func (c *udpCollector) Describe(chan<- *prometheus.Desc) {}

// Collect implements prometheus.Collector.
func (c *udpCollector) Collect(ch chan<- prometheus.Metric) {
	c.mu.Lock()
	defer c.mu.Unlock()

	for _, s := range c.series {
		m, err := prometheus.NewConstMetric(s.desc, s.family.kind.valueType(), s.value, s.labelValues...)
		if err != nil {
			c.warnOnce(s.family.name, "Cannot expose UDP metric %s: %v", s.family.name, err)
			continue
		}
		ch <- m
	}
}

// warnOnce logs a warning only the first time for each key. The caller holds c.mu.
func (c *udpCollector) warnOnce(key string, format string, args ...interface{}) {
	if c.warned[key] {
		return
	}
	c.warned[key] = true
	log.Warn().Msgf(format, args...)
}

// seriesKey returns a unique key for a metric name and a label set.
func seriesKey(name string, labels map[string]string) string {
	var b strings.Builder
	b.WriteString(name)
	for _, k := range sortedKeys(labels) {
		b.WriteByte(0xff)
		b.WriteString(k)
		b.WriteByte('=')
		b.WriteString(labels[k])
	}
	return b.String()
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// getSeries returns the series for name and labels. It makes the family and the series
// if they do not exist. It returns nil if the series is not valid, for example if the
// family has a different kind. The caller holds c.mu.
func (c *udpCollector) getSeries(name string, kind metricKind, help string, labels map[string]string) *series {
	fam, ok := c.families[name]
	if !ok {
		fam = &family{name: name, help: help, kind: kind}
		c.families[name] = fam
	}
	if fam.kind != kind {
		c.warnOnce(name, "UDP metric %s: a point needs a different metric type than the first point. The exporter ignores the point.", name)
		return nil
	}

	key := seriesKey(name, labels)
	if s, ok := c.series[key]; ok {
		return s
	}

	names := sortedKeys(labels)
	values := make([]string, len(names))
	for i, n := range names {
		values[i] = labels[n]
	}
	desc := prometheus.NewDesc(name, fam.help, names, nil)
	if _, err := prometheus.NewConstMetric(desc, kind.valueType(), 0, values...); err != nil {
		c.warnOnce(name, "Cannot register UDP metric %s with labels %v: %v", name, names, err)
		return nil
	}

	s := &series{family: fam, desc: desc, labelValues: values}
	c.series[key] = s
	return s
}

// recordPoint stores the fields of one point. fwName is the metric name of the firmware,
// without the prefix. received is the time when the exporter received the point.
func (c *udpCollector) recordPoint(p point, fwName string, received time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()

	for key, value := range p.Fields {
		metricName := p.Measurement
		if key != "v" && key != "value" {
			metricName = metricName + "_" + key
		}

		s := c.getSeries(metricName, kindGauge, gaugeHelp(fwName, key, value), p.Tags)
		if s == nil {
			continue
		}
		s.value = toFloat64(value)
		s.updated = received
	}
}

// gaugeHelp returns the help text for a numeric field.
func gaugeHelp(fwName, field string, value interface{}) string {
	if field == "v" || field == "value" {
		return fmt.Sprintf("UDP metric %q from the printer firmware, %s value.", fwName, valueTypeName(value))
	}
	return fmt.Sprintf("UDP metric %q, field %q, from the printer firmware, %s value.", fwName, field, valueTypeName(value))
}

func valueTypeName(value interface{}) string {
	switch value.(type) {
	case int64:
		return "integer"
	case float64:
		return "float"
	case bool:
		return "boolean"
	case string:
		return "string"
	default:
		return "unknown"
	}
}

func toFloat64(value interface{}) float64 {
	switch v := value.(type) {
	case int:
		return float64(v)
	case int64:
		return float64(v)
	case float64:
		return v
	case bool:
		if v {
			return 1.0
		}
		return 0.0
	case nil:
		log.Warn().Msg("Received nil value, returning 0.0")
		return 0.0
	case string:
		if v == "PLA" {
			return 1.0
		} else if v == "PETG" {
			return 2.0
		} else if v == "ASA" {
			return 3.0
		} else if v == "PC" {
			return 4.0
		} else if v == "PVB" {
			return 5.0
		} else if v == "ABS" {
			return 6.0
		} else if v == "HIPS" {
			return 7.0
		} else if v == "PP" {
			return 8.0
		} else if v == "FLEX" {
			return 9.0
		} else if v == "PA" {
			return 10.0
		} else if v == "---" {
			return -1.0 // special case for "---" to indicate no loaded filament
		} else {
			return 0.0 // return for custom
		}
	default:
		log.Warn().Msgf("Unsupported type %T for value %v", value, value)
		return 0.0
	}
}
