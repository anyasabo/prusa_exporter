package udp

import (
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

// These tests port the spec tests of the event counters from the metrics-fixes branch.

var testEvents = []string{"app_start", "crash_repeated", "maintask_loop", "modbus_reqfail", "power_panic", "probe_start"}

const testLabels = `printer_address="192.168.1.10",printer_mac="AA:BB"`

// samples returns the value of each series as "name{labels}" and the type of each name.
func samples(t *testing.T, c *Collector) (map[string]float64, map[string]dto.MetricType) {
	t.Helper()
	reg := prometheus.NewPedanticRegistry()
	reg.MustRegister(c)
	mfs, err := reg.Gather()
	if err != nil {
		t.Fatal(err)
	}
	values := map[string]float64{}
	types := map[string]dto.MetricType{}
	for _, mf := range mfs {
		types[mf.GetName()] = mf.GetType()
		for _, m := range mf.GetMetric() {
			var b strings.Builder
			b.WriteString(mf.GetName() + "{")
			for i, l := range m.GetLabel() {
				if i > 0 {
					b.WriteByte(',')
				}
				b.WriteString(l.GetName() + `="` + l.GetValue() + `"`)
			}
			b.WriteByte('}')
			key := b.String()
			switch mf.GetType() {
			case dto.MetricType_COUNTER:
				values[key] = m.GetCounter().GetValue()
			default:
				values[key] = m.GetGauge().GetValue()
			}
		}
	}
	return values, types
}

func wantSamples(t *testing.T, c *Collector, want map[string]float64) {
	t.Helper()
	got, _ := samples(t, c)
	for k, v := range want {
		g, ok := got[k]
		switch {
		case !ok:
			t.Errorf("no series %s", k)
		case g != v:
			t.Errorf("%s = %v, want %v", k, g, v)
		}
	}
}

func newEventCollector(t *testing.T) *Collector {
	t.Helper()
	c, _ := newTestCollector(t, Options{SeriesTTL: 10 * time.Minute, EventMetrics: testEvents})
	return c
}

func send(t *testing.T, c *Collector, points ...string) {
	t.Helper()
	c.Handle(t.Context(), msg(points...), printer, c.now())
}

func TestFirstPacketZeroCounters(t *testing.T) {
	t.Parallel()
	c := newEventCollector(t)
	send(t, c, "temp_bed v=60.000000 0")

	want := map[string]float64{
		"prusa_udp_points_total{" + testLabels + "}":           1,
		"prusa_udp_packets_total{" + testLabels + "}":          1,
		"prusa_udp_point_errors_all_total{" + testLabels + "}": 0,
	}
	for _, e := range testEvents {
		want["prusa_"+e+"_total{"+testLabels+"}"] = 0
	}
	wantSamples(t, c, want)
	got, types := samples(t, c)
	for _, name := range []string{"prusa_probe_start_total", "prusa_udp_points_total", "prusa_udp_packets_total", "prusa_udp_point_errors_all_total"} {
		if types[name] != dto.MetricType_COUNTER {
			t.Errorf("%s type = %v, want COUNTER", name, types[name])
		}
	}
	if hasName(types, "prusa_udp_point_errors_total") {
		t.Errorf("the labelled error counter exists before the first error: %v", got)
	}

	send(t, c, "probe_start v=T 0", "probe_start v=T 10", `tmc_read error="value_too_long" 20`)
	wantSamples(t, c, map[string]float64{
		"prusa_udp_points_total{" + testLabels + "}":                         4,
		"prusa_udp_packets_total{" + testLabels + "}":                        2,
		"prusa_udp_point_errors_all_total{" + testLabels + "}":               1,
		`prusa_udp_point_errors_total{metric="tmc_read",` + testLabels + "}": 1,
		"prusa_probe_start_total{" + testLabels + "}":                        2,
		"prusa_app_start_total{" + testLabels + "}":                          0,
	})
}

func TestUnknownEventFirstPoint(t *testing.T) {
	t.Parallel()
	c := newEventCollector(t)
	send(t, c, "temp_bed v=60 0")
	if _, types := samples(t, c); hasName(types, "prusa_new_event_total") {
		t.Fatal("prusa_new_event_total exists before its first point")
	}
	send(t, c, "new_event v=T 0")
	wantSamples(t, c, map[string]float64{"prusa_new_event_total{" + testLabels + "}": 1})
}

func TestZeroCountersNoExpiry(t *testing.T) {
	t.Parallel()
	c := newEventCollector(t)
	send(t, c, "temp_bed v=60 0")
	c.now = func() time.Time { return t0.Add(time.Hour) }

	got, _ := samples(t, c)
	if _, ok := got["prusa_temp_bed{"+testLabels+"}"]; ok {
		t.Error("the gauge prusa_temp_bed did not expire")
	}
	want := map[string]float64{
		"prusa_udp_points_total{" + testLabels + "}":           1,
		"prusa_udp_packets_total{" + testLabels + "}":          1,
		"prusa_udp_point_errors_all_total{" + testLabels + "}": 0,
	}
	for _, e := range testEvents {
		want["prusa_"+e+"_total{"+testLabels+"}"] = 0
	}
	wantSamples(t, c, want)
}

func TestZeroCountersSecondPrinter(t *testing.T) {
	t.Parallel()
	c := newEventCollector(t)
	send(t, c, "probe_start v=T 0")
	c.Handle(t.Context(), []byte("<14>1 - DEF456 buddy - - - msg=1,tm=1,v=4 temp_bed v=60.0 0\n"), netip.MustParseAddrPort("192.168.1.101:5000"), t0)
	wantSamples(t, c, map[string]float64{
		"prusa_probe_start_total{" + testLabels + "}":                                   1,
		`prusa_probe_start_total{printer_address="192.168.1.101",printer_mac="DEF456"}`: 0,
	})
}

func TestEventCountSwitchOver(t *testing.T) {
	t.Parallel()
	c := newEventCollector(t)
	key := "prusa_probe_start_total{" + testLabels + "}"

	send(t, c, "probe_start v=T 0", "probe_start v=T 10")
	wantSamples(t, c, map[string]float64{key: 2})
	send(t, c, "probe_start n=7i 0")
	wantSamples(t, c, map[string]float64{key: 7})
	send(t, c, "probe_start v=T 0")
	wantSamples(t, c, map[string]float64{key: 7})
	send(t, c, "probe_start v=T 0", "probe_start n=9i 10")
	wantSamples(t, c, map[string]float64{key: 9})
	send(t, c, "probe_start n=10i 0", "probe_start v=T 10")
	wantSamples(t, c, map[string]float64{key: 10})

	got, types := samples(t, c)
	if _, ok := got["prusa_probe_start_n{"+testLabels+"}"]; ok {
		t.Error("the count of an event metric made the gauge prusa_probe_start_n")
	}
	if types["prusa_probe_start_total"] != dto.MetricType_COUNTER {
		t.Errorf("prusa_probe_start_total type = %v, want COUNTER", types["prusa_probe_start_total"])
	}
}

func TestEventCountPrinterRestart(t *testing.T) {
	t.Parallel()
	c := newEventCollector(t)
	key := "prusa_power_panic_total{" + testLabels + "}"
	for _, n := range []string{"5", "0", "1"} {
		send(t, c, "power_panic n="+n+"i 0")
		want := map[string]float64{"5": 5, "0": 0, "1": 1}[n]
		wantSamples(t, c, map[string]float64{key: want})
	}
}

func TestEventCountUnknownEvent(t *testing.T) {
	t.Parallel()
	c := newEventCollector(t)
	send(t, c, "new_event n=3i 0")
	wantSamples(t, c, map[string]float64{"prusa_new_event_n{" + testLabels + "}": 3})
	send(t, c, "new_event v=T 0")
	send(t, c, "new_event n=4i 0")
	wantSamples(t, c, map[string]float64{"prusa_new_event_total{" + testLabels + "}": 4})
}

func TestFieldNNotEvent(t *testing.T) {
	t.Parallel()
	c := newEventCollector(t)
	send(t, c, "some_metric n=12i,x=3i 0")
	wantSamples(t, c, map[string]float64{
		"prusa_some_metric_n{" + testLabels + "}": 12,
		"prusa_some_metric_x{" + testLabels + "}": 3,
	})
	got, types := samples(t, c)
	if _, ok := got["prusa_some_metric_total{"+testLabels+"}"]; ok {
		t.Error("the field n of a metric that is not an event made a counter")
	}
	if types["prusa_some_metric_n"] != dto.MetricType_GAUGE {
		t.Errorf("prusa_some_metric_n type = %v, want GAUGE", types["prusa_some_metric_n"])
	}
}

func TestNoEventMetrics(t *testing.T) {
	t.Parallel()
	c, _ := newTestCollector(t, Options{EventMetrics: []string{}})
	send(t, c, "temp_bed v=60 0")
	if _, types := samples(t, c); hasName(types, "prusa_probe_start_total") {
		t.Errorf("an empty event list made event counters: %v", types)
	}
}

func hasName(types map[string]dto.MetricType, name string) bool {
	_, ok := types[name]
	return ok
}
