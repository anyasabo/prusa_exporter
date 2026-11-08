package prusalink

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/pstrobl96/prusa_exporter/config"
)

// mockPrinter is a PrusaLink API for Collect tests.
type mockPrinter struct {
	mu sync.Mutex
	// fail holds the paths that return HTTP 500.
	fail map[string]bool
	// requests counts the requests for each path.
	requests map[string]int
	printing bool
	// delay is the time before each response.
	delay time.Duration
}

func newMockPrinter(t *testing.T) (*mockPrinter, config.Printers) {
	t.Helper()
	m := &mockPrinter{fail: map[string]bool{}, requests: map[string]int{}}
	srv := httptest.NewServer(http.HandlerFunc(m.serve))
	t.Cleanup(srv.Close)
	return m, config.Printers{
		Address: strings.TrimPrefix(srv.URL, "http://"),
		Apikey:  "key",
		Name:    "Test",
		Type:    "Core One L",
	}
}

func (m *mockPrinter) serve(w http.ResponseWriter, r *http.Request) {
	m.mu.Lock()
	m.requests[r.URL.Path]++
	fail := m.fail[r.URL.Path]
	printing := m.printing
	delay := m.delay
	m.mu.Unlock()

	time.Sleep(delay)

	if fail {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	switch r.URL.Path {
	case "/api/version":
		fmt.Fprint(w, `{"api":"2.0.0","server":"2.1.2","text":"PrusaLink","hostname":"PrusaCOREONEL"}`)
	case "/api/job":
		fmt.Fprint(w, `{"state":"Printing","job":{"file":{"name":"a.bgcode","path":"/usb/A.BGC"}},"progress":{"completion":0.5}}`)
	case "/api/printer":
		fmt.Fprintf(w, `{"telemetry":{"print-speed":100,"material":"PLA"},"temperature":{"tool0":{"actual":215},"bed":{"actual":60}},"state":{"text":"Printing","flags":{"printing":%t,"operational":%t}}}`, printing, !printing)
	case "/api/v1/status":
		fmt.Fprint(w, `{"printer":{"fan_hotend":3000,"fan_print":5000,"flow":100}}`)
	case "/api/v1/info":
		fmt.Fprint(w, `{"nozzle_diameter":0.4,"mmu":false,"serial":"SN1","hostname":"core"}`)
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func (m *mockPrinter) count(path string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.requests[path]
}

// collectSamples runs one scrape of a new Collector and returns each series as
// "name{k=v,...}" with its value.
func collectSamples(t *testing.T, cfg config.Config) map[string]float64 {
	t.Helper()
	original := GetConfiguration()
	t.Cleanup(func() { SetConfiguration(original) })

	reg := prometheus.NewRegistry()
	reg.MustRegister(NewCollector(cfg))
	families, err := reg.Gather()
	if err != nil {
		t.Fatalf("Gather() error: %v", err)
	}
	samples := make(map[string]float64)
	for _, mf := range families {
		for _, m := range mf.GetMetric() {
			pairs := []string{}
			for _, l := range m.GetLabel() {
				pairs = append(pairs, l.GetName()+"="+l.GetValue())
			}
			sort.Strings(pairs)
			samples[mf.GetName()+"{"+strings.Join(pairs, ",")+"}"] = m.GetGauge().GetValue()
		}
	}
	return samples
}

// testConfig returns a config for one printer, with only the printer labels.
func testConfig(p config.Printers) config.Config {
	cfg := config.Config{
		Exporter: config.ExporterConfig{ScrapeTimeout: 10},
		Printers: []config.Printers{p},
	}
	cfg.PrusaLink.CommonLabels = []string{"printer_name"}
	return cfg
}

// findSample returns the value of the first series with the name.
func findSample(samples map[string]float64, name string) (float64, bool) {
	for k, v := range samples {
		if strings.HasPrefix(k, name+"{") {
			return v, true
		}
	}
	return 0, false
}

func TestCollectNozzleSizeInMeters(t *testing.T) {
	_, p := newMockPrinter(t)
	samples := collectSamples(t, testConfig(p))

	got, ok := findSample(samples, MetricPrinterNozzleSize)
	if !ok {
		t.Fatalf("%s is missing: %v", MetricPrinterNozzleSize, samples)
	}
	if got != 0.0004 {
		t.Errorf("%s = %v, want 0.0004", MetricPrinterNozzleSize, got)
	}
}

// All requests of one scrape share the scrape budget. A slow printer gives up=0
// before the budget ends.
func TestCollectScrapeBudget(t *testing.T) {
	m, p := newMockPrinter(t)
	m.delay = 300 * time.Millisecond
	cfg := testConfig(p)
	cfg.Exporter.PrusaLinkScrapeBudget = 500 * time.Millisecond

	start := time.Now()
	samples := collectSamples(t, cfg)
	if d := time.Since(start); d > 1500*time.Millisecond {
		t.Errorf("scrape took %v, want less than the budget plus one request", d)
	}
	if up, _ := findSample(samples, MetricPrinterUp); up != 0 {
		t.Errorf("%s = %v, want 0", MetricPrinterUp, up)
	}
}

// The exporter downloads the job thumbnail only if Loki is on.
func TestCollectNoThumbnailWithoutLoki(t *testing.T) {
	m, p := newMockPrinter(t)
	m.printing = true

	samples := collectSamples(t, testConfig(p))
	if status, _ := findSample(samples, MetricPrinterStatus); status != 4 {
		t.Fatalf("%s = %v, want 4 (printing)", MetricPrinterStatus, status)
	}
	time.Sleep(100 * time.Millisecond) // the download runs in a goroutine
	if n := m.count("/thumb/l/usb/A.BGC"); n != 0 {
		t.Errorf("got %d thumbnail requests with Loki off, want 0", n)
	}
}

func TestCollectThumbnailWithLoki(t *testing.T) {
	m, p := newMockPrinter(t)
	m.printing = true
	cfg := testConfig(p)
	cfg.Exporter.LokiPushURL = "http://" + p.Address + "/loki/api/v1/push"

	collectSamples(t, cfg)
	deadline := time.Now().Add(2 * time.Second)
	for m.count("/thumb/l/usb/A.BGC") == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if n := m.count("/thumb/l/usb/A.BGC"); n != 1 {
		t.Errorf("got %d thumbnail requests with Loki on, want 1", n)
	}
}
