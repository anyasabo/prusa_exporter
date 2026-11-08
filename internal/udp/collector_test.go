package udp

import (
	"context"
	"log/slog"
	"net"
	"net/netip"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

var (
	printer = netip.MustParseAddrPort("192.168.1.10:5000")
	t0      = time.Unix(1_700_000_000, 0)
)

func newTestCollector(t testing.TB, opts Options) (*Collector, *prometheus.Registry) {
	t.Helper()
	if opts.Prefix == "" {
		opts.Prefix = "prusa_"
	}
	self := prometheus.NewRegistry()
	c := NewCollector(opts, slog.New(slog.DiscardHandler), self)
	c.now = func() time.Time { return t0 }
	return c, self
}

// msg returns a firmware datagram with the points.
func msg(points ...string) []byte {
	return []byte("<14>1 - AA:BB buddy - - - msg=1,tm=1,v=4 " + strings.Join(points, "\n") + "\n")
}

// compare checks the exposition of c for the metric names.
func compare(t *testing.T, c prometheus.Collector, want string, names ...string) {
	t.Helper()
	if err := testutil.CollectAndCompare(c, strings.NewReader(want), names...); err != nil {
		t.Error(err)
	}
}

func TestCollectorKinds(t *testing.T) {
	t.Parallel()
	c, self := newTestCollector(t, Options{})
	c.Handle(msg(
		"temp_noz v=215.5 0",
		"fan,fan=print rpm=1500i,pwm=128i 10",
		"probe_start v=T 20",
		"probe_start v=T 30",
		"probe_start v=F 40",
		`fw_version v="6.4.0" 50`,
		`tmc_read error="value_too_long" 60`,
		"0x1234",
	), printer, t0)

	compare(t, c, `
# HELP prusa_fan_pwm UDP metric "fan", field "pwm", from the printer firmware, integer value.
# TYPE prusa_fan_pwm gauge
prusa_fan_pwm{fan="print",printer_address="192.168.1.10",printer_mac="AA:BB"} 128
# HELP prusa_fan_rpm UDP metric "fan", field "rpm", from the printer firmware, integer value.
# TYPE prusa_fan_rpm gauge
prusa_fan_rpm{fan="print",printer_address="192.168.1.10",printer_mac="AA:BB"} 1500
# HELP prusa_fw_version_info String UDP metric "fw_version" from the printer firmware. The label "value" holds the last string. The value is always 1.
# TYPE prusa_fw_version_info gauge
prusa_fw_version_info{printer_address="192.168.1.10",printer_mac="AA:BB",value="6.4.0"} 1
# HELP prusa_last_push_timestamp_seconds Time of the last UDP message from the printer.
# TYPE prusa_last_push_timestamp_seconds gauge
prusa_last_push_timestamp_seconds{printer_address="192.168.1.10",printer_mac="AA:BB"} 1.7e+09
# HELP prusa_probe_start_total Number of events of UDP metric "probe_start" from the printer firmware. If the firmware sends n=<count>, the value is the count of the printer since boot. Else it is the number of event points since the exporter started.
# TYPE prusa_probe_start_total counter
prusa_probe_start_total{printer_address="192.168.1.10",printer_mac="AA:BB"} 2
# HELP prusa_temp_noz UDP metric "temp_noz" from the printer firmware, float value.
# TYPE prusa_temp_noz gauge
prusa_temp_noz{printer_address="192.168.1.10",printer_mac="AA:BB"} 215.5
# HELP prusa_udp_packets_total Number of UDP datagrams from the printer that the exporter received.
# TYPE prusa_udp_packets_total counter
prusa_udp_packets_total{printer_address="192.168.1.10",printer_mac="AA:BB"} 1
# HELP prusa_udp_point_errors_all_total Number of error points (error="...") from the printer firmware, all metrics. The exporter drops these points. prusa_udp_point_errors_total has the count for each metric.
# TYPE prusa_udp_point_errors_all_total counter
prusa_udp_point_errors_all_total{printer_address="192.168.1.10",printer_mac="AA:BB"} 1
# HELP prusa_udp_points_total Number of points from the printer firmware that the exporter parsed, error points and event points included.
# TYPE prusa_udp_points_total counter
prusa_udp_points_total{printer_address="192.168.1.10",printer_mac="AA:BB"} 7
# HELP prusa_udp_point_errors_total Number of error points (error="...") from the printer firmware, by metric. The exporter drops these points.
# TYPE prusa_udp_point_errors_total counter
prusa_udp_point_errors_total{metric="tmc_read",printer_address="192.168.1.10",printer_mac="AA:BB"} 1
`)

	compare(t, self, `
# HELP prusa_exporter_udp_points_total Number of UDP points, by result. "dropped" is a point of a new series above the series limit, or with a conflicting type.
# TYPE prusa_exporter_udp_points_total counter
prusa_exporter_udp_points_total{result="dropped"} 0
prusa_exporter_udp_points_total{result="error_point"} 1
prusa_exporter_udp_points_total{result="parse_error"} 1
prusa_exporter_udp_points_total{result="stored"} 6
# HELP prusa_exporter_udp_series Number of UDP series in memory.
# TYPE prusa_exporter_udp_series gauge
prusa_exporter_udp_series 5
# HELP prusa_exporter_udp_packets_received_total Number of UDP datagrams received.
# TYPE prusa_exporter_udp_packets_received_total counter
prusa_exporter_udp_packets_received_total 1
`, "prusa_exporter_udp_points_total", "prusa_exporter_udp_series", "prusa_exporter_udp_packets_received_total")

	if problems, err := testutil.CollectAndLint(c); err != nil || len(problems) > 0 {
		t.Errorf("lint: %v %+v", err, problems)
	}
}

func TestCollectorInfoReplacesValue(t *testing.T) {
	t.Parallel()
	c, _ := newTestCollector(t, Options{})
	c.Handle(msg(`filament v="PLA" 0`), printer, t0)
	c.Handle(msg(`filament v="PETG" 0`), printer, t0)
	compare(t, c, `
# HELP prusa_filament_info String UDP metric "filament" from the printer firmware. The label "value" holds the last string. The value is always 1.
# TYPE prusa_filament_info gauge
prusa_filament_info{printer_address="192.168.1.10",printer_mac="AA:BB",value="PETG"} 1
`, "prusa_filament_info")
}

func TestCollectorVaryingTags(t *testing.T) {
	t.Parallel()
	c, _ := newTestCollector(t, Options{})
	c.Handle(msg("loadcell v=1 0", "loadcell,t=2 v=2 0"), printer, t0)
	compare(t, c, `
# HELP prusa_loadcell UDP metric "loadcell" from the printer firmware, float value.
# TYPE prusa_loadcell gauge
prusa_loadcell{printer_address="192.168.1.10",printer_mac="AA:BB"} 1
prusa_loadcell{printer_address="192.168.1.10",printer_mac="AA:BB",t="2"} 2
`, "prusa_loadcell")
}

func TestCollectorDrops(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		opts   Options
		points []string
		want   float64 // dropped points
	}{
		{"kind conflict", Options{}, []string{"x v=1 0", `x_info v=1 0`, `x v="s" 0`, "x_total v=1 0", "x v=T 0"}, 2},
		{"reserved name", Options{}, []string{"last_push_timestamp_seconds v=1 0", "udp_point_errors_total v=1 0"}, 2},
		{"series limit", Options{MaxSeries: 2}, []string{"a v=1 0", "b v=1 0", "c v=1 0", "a v=2 0"}, 1},
		{"invalid label name", Options{}, []string{"x,__bad=1 v=1 0"}, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			c, _ := newTestCollector(t, tt.opts)
			c.Handle(msg(tt.points...), printer, t0)
			if got := testutil.ToFloat64(c.metrics.points.WithLabelValues(resultDropped)); got != tt.want {
				t.Errorf("dropped = %v, want %v", got, tt.want)
			}
			// A scrape must not fail.
			reg := prometheus.NewPedanticRegistry()
			reg.MustRegister(c)
			if _, err := reg.Gather(); err != nil {
				t.Errorf("Gather() error = %v", err)
			}
		})
	}
}

func TestCollectorTTL(t *testing.T) {
	t.Parallel()
	c, _ := newTestCollector(t, Options{SeriesTTL: time.Minute})
	c.Handle(msg("temp v=1 0", `s v="x" 0`, "e v=T 0"), printer, t0)
	c.Handle(msg("fresh v=1 0"), printer, t0.Add(50*time.Second))
	c.now = func() time.Time { return t0.Add(90 * time.Second) }

	if got := testutil.CollectAndCount(c, "prusa_temp", "prusa_s_info", "prusa_e_total", "prusa_fresh"); got != 2 {
		t.Errorf("series after the TTL = %d, want 2 (the counter and the fresh gauge)", got)
	}
	if got := testutil.ToFloat64(c.metrics.seriesExpired); got != 2 {
		t.Errorf("expired = %v, want 2", got)
	}
}

func TestCollectorSampleTimestamps(t *testing.T) {
	t.Parallel()
	for _, enabled := range []bool{false, true} {
		c, _ := newTestCollector(t, Options{SampleTimestamps: enabled})
		c.Handle(msg("a v=1 0", "b v=2 500000"), printer, t0)
		reg := prometheus.NewRegistry()
		reg.MustRegister(c)
		mfs, err := reg.Gather()
		if err != nil {
			t.Fatal(err)
		}
		got := map[string]int64{}
		for _, mf := range mfs {
			got[mf.GetName()] = mf.GetMetric()[0].GetTimestampMs()
		}
		want := map[string]int64{"prusa_a": 0, "prusa_b": 0, "prusa_last_push_timestamp_seconds": 0}
		if enabled {
			want["prusa_a"] = t0.Add(-500 * time.Millisecond).UnixMilli()
			want["prusa_b"] = t0.UnixMilli()
		}
		for k, v := range want {
			if got[k] != v {
				t.Errorf("SampleTimestamps=%v: %s timestamp = %d, want %d", enabled, k, got[k], v)
			}
		}
	}
}

func TestCollectorInvalidMessage(t *testing.T) {
	t.Parallel()
	c, _ := newTestCollector(t, Options{})
	c.Handle([]byte("hello"), printer, t0)
	if got := testutil.ToFloat64(c.metrics.invalidMessages); got != 1 {
		t.Errorf("invalid messages = %v, want 1", got)
	}
	if got := testutil.CollectAndCount(c); got != 0 {
		t.Errorf("series = %d, want 0", got)
	}
}

func TestCollectorConcurrent(t *testing.T) {
	t.Parallel()
	c, _ := newTestCollector(t, Options{SeriesTTL: time.Nanosecond})
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			for range 200 {
				c.Handle(msg("a v=1 0", "e v=T 0", `s v="x" 0`), printer, t0)
				testutil.CollectAndCount(c)
			}
		})
	}
	wg.Wait()
}

func TestServe(t *testing.T) {
	t.Parallel()
	c, _ := newTestCollector(t, Options{})
	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- c.Serve(ctx, conn) }()

	client, err := net.DialUDP("udp", nil, conn.LocalAddr().(*net.UDPAddr))
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if _, err := client.Write(msg("temp_noz v=200 0")); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(5 * time.Second)
	for testutil.ToFloat64(c.metrics.packets) < 1 {
		if time.Now().After(deadline) {
			t.Fatal("the collector received no packet")
		}
		time.Sleep(10 * time.Millisecond)
	}
	compare(t, c, `
# HELP prusa_temp_noz UDP metric "temp_noz" from the printer firmware, float value.
# TYPE prusa_temp_noz gauge
prusa_temp_noz{printer_address="127.0.0.1",printer_mac="AA:BB"} 200
`, "prusa_temp_noz")

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Serve() = %v, want nil after cancel", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Serve() did not return after cancel")
	}
}
