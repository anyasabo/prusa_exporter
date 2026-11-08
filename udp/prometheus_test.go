package udp

import (
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"gopkg.in/mcuadros/go-syslog.v2/format"
)

// gatherSamples returns each series of reg as "name{k=v,...}" with its value.
// It fails the test if the registry cannot gather.
func gatherSamples(t *testing.T, reg *prometheus.Registry) map[string]float64 {
	t.Helper()
	families, err := reg.Gather()
	if err != nil {
		t.Fatalf("Gather() error: %v", err)
	}
	samples := make(map[string]float64)
	for _, mf := range families {
		for _, m := range mf.GetMetric() {
			samples[sampleKey(mf.GetName(), m)] = sampleValue(mf.GetType(), m)
		}
	}
	return samples
}

// gatherTypes returns the type of each metric family of reg.
func gatherTypes(t *testing.T, reg *prometheus.Registry) map[string]dto.MetricType {
	t.Helper()
	families, err := reg.Gather()
	if err != nil {
		t.Fatalf("Gather() error: %v", err)
	}
	types := make(map[string]dto.MetricType)
	for _, mf := range families {
		types[mf.GetName()] = mf.GetType()
	}
	return types
}

func sampleKey(name string, m *dto.Metric) string {
	pairs := make([]string, 0, len(m.GetLabel()))
	for _, l := range m.GetLabel() {
		pairs = append(pairs, l.GetName()+"="+l.GetValue())
	}
	sort.Strings(pairs)
	return name + "{" + strings.Join(pairs, ",") + "}"
}

func sampleValue(typ dto.MetricType, m *dto.Metric) float64 {
	switch typ {
	case dto.MetricType_COUNTER:
		return m.GetCounter().GetValue()
	case dto.MetricType_GAUGE:
		return m.GetGauge().GetValue()
	default:
		return m.GetUntyped().GetValue()
	}
}

// newTestRegistry calls Init with a new registry and returns the registry.
func newTestRegistry(t *testing.T) *prometheus.Registry {
	t.Helper()
	reg := prometheus.NewRegistry()
	Init(reg, Options{SeriesTTL: 10 * time.Minute})
	return reg
}

func mustParse(t *testing.T, line string) point {
	t.Helper()
	p, err := parseLineProtocol(line)
	if err != nil {
		t.Fatalf("parseLineProtocol(%q) error: %v", line, err)
	}
	return *p
}

func TestInit(t *testing.T) {
	reg := newTestRegistry(t)

	if udpRegistry != reg {
		t.Error("Init() did not set udpRegistry to provided registry")
	}
	if collector == nil {
		t.Fatal("Init() did not make the collector")
	}

	lastPush.WithLabelValues("ABC123", "192.168.1.100").Set(1)
	samples := gatherSamples(t, reg)
	if _, ok := samples["prusa_last_push_timestamp{printer_address=192.168.1.100,printer_mac=ABC123}"]; !ok {
		t.Errorf("Init() did not register prusa_last_push_timestamp, got %v", samples)
	}
}

func TestRecordPointGauges(t *testing.T) {
	reg := newTestRegistry(t)
	now := time.Now()

	lines := []string{
		"prusa_temp_bed,printer_mac=ABC123 v=60.5 0",
		"prusa_temperature,printer_mac=ABC123,sensor=nozzle value=220.5 0",
		"prusa_xbe_fan,printer_mac=ABC123,fan=1 rpm=1500i,pwm=80i 0",
		"prusa_cnt,printer_mac=ABC123 c=1 0",
	}
	for _, line := range lines {
		p := mustParse(t, line)
		collector.recordPoint(p, strings.TrimPrefix(p.Measurement, "prusa_"), now)
	}

	want := map[string]float64{
		"prusa_temp_bed{printer_mac=ABC123}":                  60.5,
		"prusa_temperature{printer_mac=ABC123,sensor=nozzle}": 220.5,
		"prusa_xbe_fan_rpm{fan=1,printer_mac=ABC123}":         1500,
		"prusa_xbe_fan_pwm{fan=1,printer_mac=ABC123}":         80,
		"prusa_cnt_c{printer_mac=ABC123}":                     1,
	}
	samples := gatherSamples(t, reg)
	for k, v := range want {
		got, ok := samples[k]
		if !ok {
			t.Errorf("series %s is missing, got %v", k, samples)
			continue
		}
		if got != v {
			t.Errorf("series %s = %v, want %v", k, got, v)
		}
	}

	types := gatherTypes(t, reg)
	if types["prusa_temp_bed"] != dto.MetricType_GAUGE {
		t.Errorf("prusa_temp_bed type = %v, want GAUGE", types["prusa_temp_bed"])
	}
}

// The firmware sends some metrics with different tag sets. Each tag set must keep
// its own labels.
func TestRecordPointVaryingTags(t *testing.T) {
	reg := newTestRegistry(t)
	now := time.Now()

	lines := []string{
		`prusa_phxy_home,printer_mac=ABC123,t="s" h=1i 0`,
		`prusa_phxy_home,printer_mac=ABC123,t="v",a=2 h=2i 0`,
		`prusa_phxy_home,printer_mac=ABC123 h=3i 0`,
	}
	for _, line := range lines {
		p := mustParse(t, line)
		collector.recordPoint(p, "phxy_home", now)
	}

	want := map[string]float64{
		"prusa_phxy_home_h{printer_mac=ABC123,t=s}":     1,
		"prusa_phxy_home_h{a=2,printer_mac=ABC123,t=v}": 2,
		"prusa_phxy_home_h{printer_mac=ABC123}":         3,
	}
	samples := gatherSamples(t, reg)
	for k, v := range want {
		if got, ok := samples[k]; !ok || got != v {
			t.Errorf("series %s = %v (present %v), want %v. All: %v", k, got, ok, v, samples)
		}
	}
}

// An event (v=T) is a counter that increments once for each point.
func TestRecordPointEvents(t *testing.T) {
	reg := newTestRegistry(t)
	now := time.Now()

	for i := 0; i < 3; i++ {
		p := mustParse(t, "prusa_probe_start,printer_mac=ABC123 v=T 0")
		collector.recordPoint(p, "probe_start", now)
	}

	samples := gatherSamples(t, reg)
	if got := samples["prusa_probe_start_total{printer_mac=ABC123}"]; got != 3 {
		t.Errorf("prusa_probe_start_total = %v, want 3. All: %v", got, samples)
	}
	if _, ok := samples["prusa_probe_start{printer_mac=ABC123}"]; ok {
		t.Error("the old gauge prusa_probe_start must not exist")
	}
	if typ := gatherTypes(t, reg)["prusa_probe_start_total"]; typ != dto.MetricType_COUNTER {
		t.Errorf("prusa_probe_start_total type = %v, want COUNTER", typ)
	}
}

// A string is an info series. A new string replaces the old label value.
func TestRecordPointStrings(t *testing.T) {
	reg := newTestRegistry(t)
	now := time.Now()

	p := mustParse(t, `prusa_filament,printer_mac=ABC123 v="PLA" 0`)
	collector.recordPoint(p, "filament", now)

	samples := gatherSamples(t, reg)
	if got := samples["prusa_filament_info{printer_mac=ABC123,value=PLA}"]; got != 1 {
		t.Errorf("prusa_filament_info{value=PLA} = %v, want 1. All: %v", got, samples)
	}
	if _, ok := samples["prusa_filament{printer_mac=ABC123}"]; ok {
		t.Error("the old gauge prusa_filament must not exist")
	}

	p = mustParse(t, `prusa_filament,printer_mac=ABC123 v="PETG" 0`)
	collector.recordPoint(p, "filament", now)

	samples = gatherSamples(t, reg)
	if _, ok := samples["prusa_filament_info{printer_mac=ABC123,value=PLA}"]; ok {
		t.Errorf("the old string PLA is still a series: %v", samples)
	}
	if got := samples["prusa_filament_info{printer_mac=ABC123,value=PETG}"]; got != 1 {
		t.Errorf("prusa_filament_info{value=PETG} = %v, want 1. All: %v", got, samples)
	}

	// A string field of a custom metric.
	p = mustParse(t, `prusa_tmc_read,printer_mac=ABC123,ax=X reg=1i,regn="GCONF",value=5i 0`)
	collector.recordPoint(p, "tmc_read", now)
	samples = gatherSamples(t, reg)
	if got := samples["prusa_tmc_read_regn_info{ax=X,printer_mac=ABC123,value=GCONF}"]; got != 1 {
		t.Errorf("prusa_tmc_read_regn_info = %v, want 1. All: %v", got, samples)
	}
	if got := samples["prusa_tmc_read{ax=X,printer_mac=ABC123}"]; got != 5 {
		t.Errorf("prusa_tmc_read = %v, want 5. All: %v", got, samples)
	}
}

// processLines sends one message with lines to process, as the printer does.
func processLines(lines ...string) {
	msg := "msg=1,tm=1000000,v=4 " + strings.Join(lines, "\n") + "\n"
	process(format.LogParts{
		"hostname": "ABC123",
		"client":   "192.168.1.100:5000",
		"message":  msg,
	}, "prusa_")
}

// An error point is dropped and counted. It must not make a series.
func TestProcessErrorPoints(t *testing.T) {
	reg := newTestRegistry(t)

	processLines(
		`temp_bed v=60.000000 10`,
		`tmc_read error="value_too_long" 20`,
		`tmc_read error="a, b" 30`,
		`fsensor error="value too long" 40`,
	)

	samples := gatherSamples(t, reg)
	labels := "printer_address=192.168.1.100,printer_mac=ABC123"
	want := map[string]float64{
		"prusa_temp_bed{" + labels + "}":                               60,
		"prusa_udp_point_errors_total{metric=tmc_read," + labels + "}": 2,
		"prusa_udp_point_errors_total{metric=fsensor," + labels + "}":  1,
	}
	for k, v := range want {
		if got, ok := samples[k]; !ok || got != v {
			t.Errorf("series %s = %v (present %v), want %v. All: %v", k, got, ok, v, samples)
		}
	}
	for k := range samples {
		if strings.Contains(k, "_error{") {
			t.Errorf("error point made the series %s", k)
		}
	}
}

// A gauge or info series without a new point in the TTL goes away. A counter stays.
func TestSeriesTTL(t *testing.T) {
	reg := newTestRegistry(t)
	start := time.Now()
	now := start
	collector.now = func() time.Time { return now }

	record := func(line, fwName string) {
		collector.recordPoint(mustParse(t, line), fwName, now)
	}
	record("prusa_temp_bed,printer_mac=ABC123 v=60.0 0", "temp_bed")
	record(`prusa_fw_version,printer_mac=ABC123 v="6.4.0" 0`, "fw_version")
	record("prusa_crash_repeated,printer_mac=ABC123 v=T 0", "crash_repeated")
	record("prusa_temp_noz,printer_mac=ABC123 v=215.0 0", "temp_noz")

	// 9 minutes later, only temp_noz gets a new point.
	now = start.Add(9 * time.Minute)
	record("prusa_temp_noz,printer_mac=ABC123 v=216.0 0", "temp_noz")
	if got := len(gatherSamples(t, reg)); got != 4 {
		t.Errorf("after 9 min: got %d series, want 4", got)
	}

	// 11 minutes after the start, temp_bed and fw_version are older than the TTL.
	now = start.Add(11 * time.Minute)
	samples := gatherSamples(t, reg)
	for _, k := range []string{
		"prusa_temp_bed{printer_mac=ABC123}",
		"prusa_fw_version_info{printer_mac=ABC123,value=6.4.0}",
	} {
		if _, ok := samples[k]; ok {
			t.Errorf("series %s did not expire", k)
		}
	}
	for _, k := range []string{
		"prusa_temp_noz{printer_mac=ABC123}",
		"prusa_crash_repeated_total{printer_mac=ABC123}",
	} {
		if _, ok := samples[k]; !ok {
			t.Errorf("series %s expired, want it to stay. All: %v", k, samples)
		}
	}

	// A new point makes the series again.
	record("prusa_temp_bed,printer_mac=ABC123 v=61.0 0", "temp_bed")
	if got := gatherSamples(t, reg)["prusa_temp_bed{printer_mac=ABC123}"]; got != 61 {
		t.Errorf("prusa_temp_bed = %v, want 61", got)
	}
}

// With SampleTimestamps, each sample has the estimated printer time. The last point
// of a message has the receive time. The other points are earlier by their offset.
func TestSampleTimestamps(t *testing.T) {
	reg := prometheus.NewRegistry()
	Init(reg, Options{SeriesTTL: 10 * time.Minute, SampleTimestamps: true})

	before := time.Now()
	processLines(
		`temp_bed v=60.000000 0`,
		`temp_noz v=215.000000 500000`,
		`probe_start v=T 1000000`,
	)
	after := time.Now()

	families, err := reg.Gather()
	if err != nil {
		t.Fatal(err)
	}
	ts := make(map[string]int64)
	for _, mf := range families {
		for _, m := range mf.GetMetric() {
			ts[mf.GetName()] = m.GetTimestampMs()
		}
	}

	last := ts["prusa_probe_start_total"]
	if last < before.UnixMilli() || last > after.UnixMilli() {
		t.Errorf("last point time %d is not the receive time (%d to %d)", last, before.UnixMilli(), after.UnixMilli())
	}
	if d := last - ts["prusa_temp_noz"]; d != 500 {
		t.Errorf("temp_noz is %d ms before the last point, want 500", d)
	}
	if d := last - ts["prusa_temp_bed"]; d != 1000 {
		t.Errorf("temp_bed is %d ms before the last point, want 1000", d)
	}
	if ts["prusa_last_push_timestamp"] != 0 {
		t.Error("prusa_last_push_timestamp must not have a timestamp")
	}
}

// Without SampleTimestamps, no sample has a timestamp.
func TestNoSampleTimestamps(t *testing.T) {
	reg := newTestRegistry(t)
	processLines(`temp_bed v=60.000000 0`)

	families, err := reg.Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, mf := range families {
		for _, m := range mf.GetMetric() {
			if m.TimestampMs != nil {
				t.Errorf("%s has a timestamp", mf.GetName())
			}
		}
	}
}

// A point that needs a different type than the first point of the metric, or that has
// a reserved name, is ignored. The scrape must not fail.
func TestRecordPointConflicts(t *testing.T) {
	reg := newTestRegistry(t)
	now := time.Now()

	record := func(line, fwName string) {
		collector.recordPoint(mustParse(t, line), fwName, now)
	}
	record("prusa_x,printer_mac=ABC123 v=1.5 0", "x")
	record(`prusa_x_info,printer_mac=ABC123 v=2.5 0`, "x_info")
	record(`prusa_x,printer_mac=ABC123 v="text" 0`, "x")
	record("prusa_udp_point_errors v=T 0", "udp_point_errors")
	record("prusa_last_push_timestamp v=1.0 0", "last_push_timestamp")

	samples := gatherSamples(t, reg)
	if got := samples["prusa_x{printer_mac=ABC123}"]; got != 1.5 {
		t.Errorf("prusa_x = %v, want 1.5", got)
	}
	if got := samples["prusa_x_info{printer_mac=ABC123}"]; got != 2.5 {
		t.Errorf("prusa_x_info = %v, want 2.5 (the first point wins). All: %v", got, samples)
	}
	if _, ok := samples["prusa_x_info{printer_mac=ABC123,value=text}"]; ok {
		t.Error("the string point must not join the gauge family prusa_x_info")
	}
}

func TestConcurrentRecordPoint(t *testing.T) {
	reg := newTestRegistry(t)

	done := make(chan bool, 10)
	for i := 0; i < 10; i++ {
		go func(id int) {
			defer func() { done <- true }()
			p := point{
				Measurement: "concurrent_test",
				Tags:        map[string]string{"printer_mac": "ABC123", "id": fmt.Sprint(id)},
				Fields:      map[string]interface{}{"value": float64(id)},
			}
			collector.recordPoint(p, "concurrent_test", time.Now())
			_, _ = reg.Gather()
		}(i)
	}
	for i := 0; i < 10; i++ {
		<-done
	}

	samples := gatherSamples(t, reg)
	if len(samples) != 10 {
		t.Errorf("got %d series, want 10: %v", len(samples), samples)
	}
}

func TestToFloat64(t *testing.T) {
	tests := []struct {
		name     string
		input    interface{}
		expected float64
	}{
		{"int", 42, 42.0},
		{"int64", int64(123), 123.0},
		{"float64", 3.14, 3.14},
		{"bool true", true, 1.0},
		{"bool false", false, 0.0},
		{"nil", nil, 0.0},
		{"unsupported type", []int{1, 2, 3}, 0.0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := toFloat64(tt.input)
			if result != tt.expected {
				t.Errorf("toFloat64(%v) = %v, expected %v", tt.input, result, tt.expected)
			}
		})
	}
}

func BenchmarkRecordPoint(b *testing.B) {
	Init(prometheus.NewRegistry(), Options{})

	p := point{
		Measurement: "benchmark_metric",
		Tags:        map[string]string{"printer_mac": "ABC123", "printer_address": "192.168.1.100"},
		Fields:      map[string]interface{}{"value": 220.5, "target": 225.0},
	}
	now := time.Now()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		collector.recordPoint(p, "benchmark_metric", now)
	}
}

// testEvents is the default list of known events in prusa.yml.
var testEvents = []string{"app_start", "crash_repeated", "maintask_loop", "modbus_reqfail", "power_panic", "probe_start"}

const testLabels = "printer_address=192.168.1.100,printer_mac=ABC123"

// newEventRegistry calls Init with the known events and a fake clock.
// It returns the registry and a pointer to the clock time.
func newEventRegistry(t *testing.T) (*prometheus.Registry, *time.Time) {
	t.Helper()
	reg := prometheus.NewRegistry()
	Init(reg, Options{SeriesTTL: 10 * time.Minute, EventMetrics: testEvents})
	now := time.Now()
	collector.now = func() time.Time { return now }
	return reg, &now
}

// wantSamples fails the test for each series in want that is missing or has a
// different value.
func wantSamples(t *testing.T, reg *prometheus.Registry, want map[string]float64) {
	t.Helper()
	samples := gatherSamples(t, reg)
	for k, v := range want {
		if got, ok := samples[k]; !ok || got != v {
			t.Errorf("series %s = %v (present %v), want %v. All: %v", k, got, ok, v, samples)
		}
	}
}

// The first packet of a printer makes the known event counters and the totals at 0.
// Later points increment them.
func TestFirstPacketZeroCounters(t *testing.T) {
	reg, _ := newEventRegistry(t)

	processLines(`temp_bed v=60.000000 0`)

	want := map[string]float64{
		"prusa_udp_points_total{" + testLabels + "}":           1,
		"prusa_udp_packets_total{" + testLabels + "}":          1,
		"prusa_udp_point_errors_all_total{" + testLabels + "}": 0,
	}
	for _, e := range testEvents {
		want["prusa_"+e+"_total{"+testLabels+"}"] = 0
	}
	wantSamples(t, reg, want)
	types := gatherTypes(t, reg)
	for _, name := range []string{"prusa_probe_start_total", "prusa_udp_points_total", "prusa_udp_packets_total", "prusa_udp_point_errors_all_total"} {
		if types[name] != dto.MetricType_COUNTER {
			t.Errorf("%s type = %v, want COUNTER", name, types[name])
		}
	}
	if _, ok := gatherSamples(t, reg)["prusa_udp_point_errors_total{metric=,"+testLabels+"}"]; ok {
		t.Error("the labelled error counter must not have a series with metric=\"\"")
	}

	processLines(
		`probe_start v=T 0`,
		`probe_start v=T 10`,
		`tmc_read error="value_too_long" 20`,
	)
	want = map[string]float64{
		"prusa_udp_points_total{" + testLabels + "}":                       4,
		"prusa_udp_packets_total{" + testLabels + "}":                      2,
		"prusa_udp_point_errors_all_total{" + testLabels + "}":             1,
		"prusa_udp_point_errors_total{metric=tmc_read," + testLabels + "}": 1,
		"prusa_probe_start_total{" + testLabels + "}":                      2,
		"prusa_app_start_total{" + testLabels + "}":                        0,
	}
	wantSamples(t, reg, want)
}

// An event that is not in the list gets its counter with its first point.
func TestUnknownEventFirstPoint(t *testing.T) {
	reg, _ := newEventRegistry(t)

	processLines(`temp_bed v=60.000000 0`)
	if _, ok := gatherSamples(t, reg)["prusa_new_event_total{"+testLabels+"}"]; ok {
		t.Fatal("prusa_new_event_total exists before its first point")
	}
	processLines(`new_event v=T 0`)
	wantSamples(t, reg, map[string]float64{"prusa_new_event_total{" + testLabels + "}": 1})
}

// The zero counters and the totals do not expire.
func TestZeroCountersNoExpiry(t *testing.T) {
	reg, now := newEventRegistry(t)

	processLines(`temp_bed v=60.000000 0`)
	*now = now.Add(time.Hour)

	samples := gatherSamples(t, reg)
	if _, ok := samples["prusa_temp_bed{"+testLabels+"}"]; ok {
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
	wantSamples(t, reg, want)
}

// A second printer gets its own zero counters. The first printer keeps its counts.
func TestZeroCountersSecondPrinter(t *testing.T) {
	reg, _ := newEventRegistry(t)

	processLines(`probe_start v=T 0`)
	process(format.LogParts{
		"hostname": "DEF456",
		"client":   "192.168.1.101:5000",
		"message":  "msg=1,tm=1000000,v=4 temp_bed v=60.0 0\n",
	}, "prusa_")

	wantSamples(t, reg, map[string]float64{
		"prusa_probe_start_total{" + testLabels + "}":                               1,
		"prusa_probe_start_total{printer_address=192.168.1.101,printer_mac=DEF456}": 0,
	})
}

// A count point (n=<int>) of an event metric sets the counter to the count of the
// printer. After it, v=T points do not change the counter.
func TestEventCountSwitchOver(t *testing.T) {
	reg, _ := newEventRegistry(t)
	key := "prusa_probe_start_total{" + testLabels + "}"

	processLines(`probe_start v=T 0`, `probe_start v=T 10`)
	wantSamples(t, reg, map[string]float64{key: 2})

	processLines(`probe_start n=7i 0`)
	wantSamples(t, reg, map[string]float64{key: 7})

	processLines(`probe_start v=T 0`)
	wantSamples(t, reg, map[string]float64{key: 7})

	processLines(`probe_start v=T 0`, `probe_start n=9i 10`)
	wantSamples(t, reg, map[string]float64{key: 9})

	samples := gatherSamples(t, reg)
	if _, ok := samples["prusa_probe_start_n{"+testLabels+"}"]; ok {
		t.Error("the count of an event metric must not make the gauge prusa_probe_start_n")
	}
	if typ := gatherTypes(t, reg)["prusa_probe_start_total"]; typ != dto.MetricType_COUNTER {
		t.Errorf("prusa_probe_start_total type = %v, want COUNTER", typ)
	}
}

// After a printer restart, the count of the printer goes down. The counter takes the
// lower value, and rate() sees a counter reset.
func TestEventCountPrinterRestart(t *testing.T) {
	reg, _ := newEventRegistry(t)
	key := "prusa_power_panic_total{" + testLabels + "}"

	processLines(`power_panic n=5i 0`)
	wantSamples(t, reg, map[string]float64{key: 5})
	processLines(`power_panic n=0i 0`)
	wantSamples(t, reg, map[string]float64{key: 0})
	processLines(`power_panic n=1i 0`)
	wantSamples(t, reg, map[string]float64{key: 1})
}

// An event metric that is not in the list becomes a count metric after its first v=T
// point. Before that, its field n is a gauge.
func TestEventCountUnknownEvent(t *testing.T) {
	reg, _ := newEventRegistry(t)

	processLines(`new_event n=3i 0`)
	wantSamples(t, reg, map[string]float64{"prusa_new_event_n{" + testLabels + "}": 3})

	processLines(`new_event v=T 0`)
	processLines(`new_event n=4i 0`)
	wantSamples(t, reg, map[string]float64{"prusa_new_event_total{" + testLabels + "}": 4})
}

// For a metric that is not an event metric, the field n stays a gauge.
func TestFieldNNotEvent(t *testing.T) {
	reg, _ := newEventRegistry(t)

	processLines(`some_metric n=12i,x=3i 0`)
	wantSamples(t, reg, map[string]float64{
		"prusa_some_metric_n{" + testLabels + "}": 12,
		"prusa_some_metric_x{" + testLabels + "}": 3,
	})
	if _, ok := gatherSamples(t, reg)["prusa_some_metric_total{"+testLabels+"}"]; ok {
		t.Error("the field n of a metric that is not an event made a counter")
	}
	if typ := gatherTypes(t, reg)["prusa_some_metric_n"]; typ != dto.MetricType_GAUGE {
		t.Errorf("prusa_some_metric_n type = %v, want GAUGE", typ)
	}
}
