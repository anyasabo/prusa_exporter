package prusalink

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	dto "github.com/prometheus/client_model/go"
)

const printingExposition = `
# HELP prusa_axis_position_meters Position of an axis.
# TYPE prusa_axis_position_meters gauge
prusa_axis_position_meters{printer_address="ADDR",printer_axis="x",printer_model="COREONEL",printer_name="Test"} 0.1205
prusa_axis_position_meters{printer_address="ADDR",printer_axis="y",printer_model="COREONEL",printer_name="Test"} 0.08025
prusa_axis_position_meters{printer_address="ADDR",printer_axis="z",printer_model="COREONEL",printer_name="Test"} 0.0012
# HELP prusa_fan_speed_rpm Speed of a fan in revolutions per minute.
# TYPE prusa_fan_speed_rpm gauge
prusa_fan_speed_rpm{fan="hotend",printer_address="ADDR",printer_model="COREONEL",printer_name="Test"} 3500
prusa_fan_speed_rpm{fan="print",printer_address="ADDR",printer_model="COREONEL",printer_name="Test"} 5000
# HELP prusa_info Information about the printer. The value is always 1.
# TYPE prusa_info gauge
prusa_info{api_version="2.0.0",printer_address="ADDR",printer_hostname="core-one",printer_location="Workshop",printer_model="COREONEL",printer_name="Test",prusalink_name="Core One L",serial_number="CZPX1234X017XC12345",server_version="2.1.2",version_text="PrusaLink"} 1
# HELP prusa_job_info The current print job. The value is always 1. There is no series if there is no job.
# TYPE prusa_job_info gauge
prusa_job_info{printer_address="ADDR",printer_job_name="benchy_0.4n_0.2mm_PLA.bgcode",printer_job_path="/usb/BENCHY~1.BGC",printer_model="COREONEL",printer_name="Test"} 1
# HELP prusa_material_info The loaded filament ("---" if none). The value is always 1.
# TYPE prusa_material_info gauge
prusa_material_info{printer_address="ADDR",printer_filament="PLA",printer_model="COREONEL",printer_name="Test"} 1
# HELP prusa_mmu_enabled 1 if the MMU is enabled, else 0.
# TYPE prusa_mmu_enabled gauge
prusa_mmu_enabled{printer_address="ADDR",printer_model="COREONEL",printer_name="Test"} 0
# HELP prusa_nozzle_size_meters Diameter of the selected nozzle.
# TYPE prusa_nozzle_size_meters gauge
prusa_nozzle_size_meters{printer_address="ADDR",printer_model="COREONEL",printer_name="Test"} 0.0004
# HELP prusa_print_flow_ratio Flow factor of the printer, 1 is 100 %.
# TYPE prusa_print_flow_ratio gauge
prusa_print_flow_ratio{printer_address="ADDR",printer_model="COREONEL",printer_name="Test"} 0.95
# HELP prusa_print_speed_ratio Speed factor of the printer, 1 is 100 %.
# TYPE prusa_print_speed_ratio gauge
prusa_print_speed_ratio{printer_address="ADDR",printer_model="COREONEL",printer_name="Test"} 1
# HELP prusa_print_time_seconds Time since the start of the current print.
# TYPE prusa_print_time_seconds gauge
prusa_print_time_seconds{printer_address="ADDR",printer_model="COREONEL",printer_name="Test"} 1200
# HELP prusa_printer_state 1 for the current state of the printer, else 0.
# TYPE prusa_printer_state gauge
prusa_printer_state{printer_address="ADDR",printer_model="COREONEL",printer_name="Test",printer_state="ATTENTION"} 0
prusa_printer_state{printer_address="ADDR",printer_model="COREONEL",printer_name="Test",printer_state="BUSY"} 0
prusa_printer_state{printer_address="ADDR",printer_model="COREONEL",printer_name="Test",printer_state="ERROR"} 0
prusa_printer_state{printer_address="ADDR",printer_model="COREONEL",printer_name="Test",printer_state="FINISHED"} 0
prusa_printer_state{printer_address="ADDR",printer_model="COREONEL",printer_name="Test",printer_state="IDLE"} 0
prusa_printer_state{printer_address="ADDR",printer_model="COREONEL",printer_name="Test",printer_state="PAUSED"} 0
prusa_printer_state{printer_address="ADDR",printer_model="COREONEL",printer_name="Test",printer_state="PRINTING"} 1
prusa_printer_state{printer_address="ADDR",printer_model="COREONEL",printer_name="Test",printer_state="READY"} 0
prusa_printer_state{printer_address="ADDR",printer_model="COREONEL",printer_name="Test",printer_state="STOPPED"} 0
prusa_printer_state{printer_address="ADDR",printer_model="COREONEL",printer_name="Test",printer_state="UNKNOWN"} 0
# HELP prusa_printing_progress_ratio Progress of the current print, from 0 to 1.
# TYPE prusa_printing_progress_ratio gauge
prusa_printing_progress_ratio{printer_address="ADDR",printer_model="COREONEL",printer_name="Test"} 0.37
# HELP prusa_printing_time_remaining_seconds Estimated time until the end of the current print.
# TYPE prusa_printing_time_remaining_seconds gauge
prusa_printing_time_remaining_seconds{printer_address="ADDR",printer_model="COREONEL",printer_name="Test"} 3600
# HELP prusa_temperature_celsius Temperature of a heated element.
# TYPE prusa_temperature_celsius gauge
prusa_temperature_celsius{printer_address="ADDR",printer_heated_element="bed",printer_model="COREONEL",printer_name="Test"} 60.1
prusa_temperature_celsius{printer_address="ADDR",printer_heated_element="tool0",printer_model="COREONEL",printer_name="Test"} 215.3
# HELP prusa_temperature_target_celsius Target temperature of a heated element.
# TYPE prusa_temperature_target_celsius gauge
prusa_temperature_target_celsius{printer_address="ADDR",printer_heated_element="bed",printer_model="COREONEL",printer_name="Test"} 60
prusa_temperature_target_celsius{printer_address="ADDR",printer_heated_element="tool0",printer_model="COREONEL",printer_name="Test"} 215
# HELP prusa_udp_metrics_gcode_sent 1 if the exporter sent and started the G-code that enables the UDP metrics, else 0.
# TYPE prusa_udp_metrics_gcode_sent gauge
prusa_udp_metrics_gcode_sent{printer_address="ADDR",printer_model="COREONEL",printer_name="Test"} 0
# HELP prusa_up 1 if all PrusaLink requests of the scrape succeeded, else 0.
# TYPE prusa_up gauge
prusa_up{printer_address="ADDR",printer_model="COREONEL",printer_name="Test"} 1
`

// gather scrapes e one time and returns a registry with the result.
func gather(t *testing.T, e *Exporter) prometheus.Gatherer {
	t.Helper()
	reg := prometheus.NewPedanticRegistry()
	reg.MustRegister(&scrape{ctx: t.Context(), budget: 5 * time.Second, e: e})
	mfs, err := reg.Gather()
	if err != nil {
		t.Fatalf("Gather() error = %v", err)
	}
	return prometheus.GathererFunc(func() ([]*dto.MetricFamily, error) { return mfs, nil })
}

func newTestExporter(t *testing.T, f *fakePrusaLink, opts Options) (*Exporter, *Target) {
	t.Helper()
	target := &Target{Client: NewClient(f.printer(), NewTransport(), NewClientMetrics(prometheus.NewRegistry()))}
	return NewExporter([]*Target{target}, opts, discardLogger(), prometheus.NewRegistry()), target
}

func TestExporterPrinting(t *testing.T) {
	t.Parallel()
	for _, digest := range []bool{true, false} {
		f := newFakePrusaLink(t, digest)
		e, _ := newTestExporter(t, f, Options{})
		want := strings.ReplaceAll(printingExposition, "ADDR", f.printer().Address)
		g := gather(t, e)
		if err := testutil.GatherAndCompare(g, strings.NewReader(want), MetricNamesExcept("prusa_scrape_duration_seconds")...); err != nil {
			t.Errorf("digest=%v: %v", digest, err)
		}
		if problems, err := testutil.GatherAndLint(g); err != nil || len(problems) > 0 {
			t.Errorf("lint: %v %+v", err, problems)
		}
	}
}

func TestExporterIdle(t *testing.T) {
	t.Parallel()
	f := newFakePrusaLink(t, false)
	f.set("/api/v1/status", "status_idle.json")
	e, _ := newTestExporter(t, f, Options{})
	g := gather(t, e)
	if n, _ := testutil.GatherAndCount(g, "prusa_job_info"); n != 0 {
		t.Errorf("prusa_job_info has %d series while idle, want 0", n)
	}
	if got := f.count("GET /api/job"); got != 0 {
		t.Errorf("GET /api/job sent %d times while idle, want 0", got)
	}
	want := strings.ReplaceAll(`
# HELP prusa_printing_progress_ratio Progress of the current print, from 0 to 1.
# TYPE prusa_printing_progress_ratio gauge
prusa_printing_progress_ratio{printer_address="ADDR",printer_model="COREONEL",printer_name="Test"} 0
# HELP prusa_up 1 if all PrusaLink requests of the scrape succeeded, else 0.
# TYPE prusa_up gauge
prusa_up{printer_address="ADDR",printer_model="COREONEL",printer_name="Test"} 1
`, "ADDR", f.printer().Address)
	if err := testutil.GatherAndCompare(g, strings.NewReader(want), "prusa_printing_progress_ratio", "prusa_up"); err != nil {
		t.Error(err)
	}
}

func TestExporterUnknownState(t *testing.T) {
	t.Parallel()
	f := newFakePrusaLink(t, false)
	f.setRaw("/api/v1/status", `{"printer":{"state":"FUTURE"}}`)
	e, _ := newTestExporter(t, f, Options{})
	// A state that is not in States gets its own series.
	n, err := testutil.GatherAndCount(gather(t, e), "prusa_printer_state")
	if err != nil || n != len(States)+1 {
		t.Errorf("prusa_printer_state has %d series (err %v), want %d", n, err, len(States)+1)
	}
}

func TestExporterRequestFails(t *testing.T) {
	t.Parallel()
	for _, path := range []string{"/api/v1/status", "/api/v1/info", "/api/version", "/api/printer", "/api/job"} {
		t.Run(path, func(t *testing.T) {
			t.Parallel()
			f := newFakePrusaLink(t, false)
			f.setCode(path, http.StatusInternalServerError)
			e, _ := newTestExporter(t, f, Options{})
			g := gather(t, e)
			want := strings.ReplaceAll(`
# HELP prusa_up 1 if all PrusaLink requests of the scrape succeeded, else 0.
# TYPE prusa_up gauge
prusa_up{printer_address="ADDR",printer_model="COREONEL",printer_name="Test"} 0
`, "ADDR", f.printer().Address)
			if err := testutil.GatherAndCompare(g, strings.NewReader(want), "prusa_up"); err != nil {
				t.Error(err)
			}
			// Only prusa_up, prusa_scrape_duration_seconds and prusa_udp_metrics_gcode_sent.
			if n, _ := testutil.GatherAndCount(g); n != 3 {
				t.Errorf("%d series after a failed request, want 3", n)
			}
		})
	}
}

func TestExporterBadJSON(t *testing.T) {
	t.Parallel()
	f := newFakePrusaLink(t, false)
	f.setRaw("/api/version", `{"api": 2`)
	e, _ := newTestExporter(t, f, Options{})
	g := gather(t, e)
	if n, _ := testutil.GatherAndCount(g, "prusa_info"); n != 0 {
		t.Errorf("prusa_info exposed after a decode error")
	}
	if n, _ := testutil.GatherAndCount(g, "prusa_up"); n != 1 {
		t.Errorf("no prusa_up after a decode error")
	}
}

func TestExporterLabelsAndDisable(t *testing.T) {
	t.Parallel()
	f := newFakePrusaLink(t, false)
	e, _ := newTestExporter(t, f, Options{
		CommonLabels:   []string{"printer_name", "printer_job_name"},
		DisableMetrics: []string{"prusa_info", "prusa_temperature_celsius"},
	})
	g := gather(t, e)
	for name, want := range map[string]int{"prusa_info": 0, "prusa_temperature_celsius": 0, "prusa_temperature_target_celsius": 2} {
		if n, _ := testutil.GatherAndCount(g, name); n != want {
			t.Errorf("%s has %d series, want %d", name, n, want)
		}
	}
	want := `
# HELP prusa_mmu_enabled 1 if the MMU is enabled, else 0.
# TYPE prusa_mmu_enabled gauge
prusa_mmu_enabled{printer_job_name="benchy_0.4n_0.2mm_PLA.bgcode",printer_name="Test"} 0
`
	if err := testutil.GatherAndCompare(g, strings.NewReader(want), "prusa_mmu_enabled"); err != nil {
		t.Error(err)
	}
}

func TestDigestChallengeIsReused(t *testing.T) {
	t.Parallel()
	f := newFakePrusaLink(t, true)
	e, _ := newTestExporter(t, f, Options{})
	for range 3 {
		gather(t, e)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.challenges != 1 {
		t.Errorf("%d digest challenges in 3 scrapes, want 1", f.challenges)
	}
	if got := f.requests["GET /api/v1/status"]; got != 3 {
		t.Errorf("%d authenticated status requests, want 3", got)
	}
}

func TestHandlerScrapeTimeout(t *testing.T) {
	t.Parallel()
	block := make(chan struct{})
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-block:
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(func() { close(block); slow.Close() })

	target := &Target{Client: NewClient(newFakePrusaLinkPrinter(slow.URL), NewTransport(), nil)}
	self := prometheus.NewRegistry()
	e := NewExporter([]*Target{target}, Options{Budget: time.Minute}, discardLogger(), self)

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/metrics/prusalink", nil)
	req.Header.Set("X-Prometheus-Scrape-Timeout-Seconds", "1")
	rec := httptest.NewRecorder()
	start := time.Now()
	e.Handler().ServeHTTP(rec, req)
	if d := time.Since(start); d > 3*time.Second {
		t.Errorf("the scrape took %v, want less than the scrape timeout", d)
	}
	if !strings.Contains(rec.Body.String(), "prusa_up{") || !strings.Contains(rec.Body.String(), "} 0") {
		t.Errorf("body has no prusa_up 0:\n%s", rec.Body.String())
	}
	if n, _ := testutil.GatherAndCount(self, "prusa_exporter_prusalink_handler_errors_total"); n != 1 {
		t.Errorf("the handler error counter is not registered")
	}
}

func TestScrapeBudget(t *testing.T) {
	t.Parallel()
	tests := []struct {
		header string
		want   time.Duration
	}{
		{"", 8 * time.Second},
		{"bad", 8 * time.Second},
		{"-1", 8 * time.Second},
		{"10", 8 * time.Second},
		{"5", 4500 * time.Millisecond},
		{"0.25", 8 * time.Second}, // shorter than the offset: keep the budget
	}
	for _, tt := range tests {
		if got := scrapeBudget(8*time.Second, tt.header); got != tt.want {
			t.Errorf("scrapeBudget(8s, %q) = %v, want %v", tt.header, got, tt.want)
		}
	}
}

func TestClientMetrics(t *testing.T) {
	t.Parallel()
	f := newFakePrusaLink(t, true)
	reg := prometheus.NewRegistry()
	c := NewClient(f.printer(), NewTransport(), NewClientMetrics(reg))
	if _, err := c.Status(context.Background()); err != nil {
		t.Fatal(err)
	}
	// The challenge (401) and the authenticated request.
	if n, err := testutil.GatherAndCount(reg, "prusa_exporter_prusalink_request_duration_seconds"); err != nil || n != 2 {
		t.Errorf("request histogram has %d series (err %v), want 2 (401 and 200)", n, err)
	}
}
