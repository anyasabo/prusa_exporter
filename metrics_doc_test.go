package main

import (
	"flag"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/common/expfmt"
	"github.com/prometheus/common/model"
)

var update = flag.Bool("update", false, "write the golden files again")

const metricsDoc = "docs/metrics.md"

// TestMetricsReference makes docs/metrics.md from the real output of the exporter. It
// is also the contract of the metric names, types and labels: a change of the output
// changes the file, and the test fails until the file is written again with -update.
func TestMetricsReference(t *testing.T) {
	root, err := os.OpenRoot("internal/prusalink/testdata")
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	fixtures := map[string]string{
		"/api/v1/status": "status_printing.json",
		"/api/v1/info":   "info.json",
		"/api/version":   "version.json",
		"/api/printer":   "printer.json",
		"/api/job":       "job.json",
	}
	printer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name, ok := fixtures[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		data, err := root.ReadFile(name)
		if err != nil {
			t.Error(err)
		}
		_, _ = w.Write(data)
	}))
	defer printer.Close()
	loki := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer loki.Close()

	cfg := "printers:\n  - address: " + strings.TrimPrefix(printer.URL, "http://") +
		"\n    apikey: k\n    name: P\n    type: MK4\nexporter:\n  udp_event_metrics: []\n"
	base, udpAddr, stop := startExporter(t, cfg, "--loki.push-url="+loki.URL)
	defer func() { _ = stop() }()

	// One error point makes all fixed UDP series. The firmware metrics depend on the
	// firmware, so the reference does not list them.
	conn, err := (&net.Dialer{}).DialContext(t.Context(), "udp", udpAddr)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.Write([]byte("<14>1 - AA:BB buddy - - - msg=1,tm=1,v=4 tmc_read error=\"x\" 0\n")); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for !strings.Contains(get(t, base+"/metrics/udp"), "prusa_udp_point_errors_total{") {
		if time.Now().After(deadline) {
			t.Fatal("no UDP series")
		}
		time.Sleep(20 * time.Millisecond)
	}

	var b strings.Builder
	b.WriteString(`# Metrics reference

This file is generated. Do not edit it. To write it again, run:

    mise exec -- go test -run TestMetricsReference . -args -update

The test reads the output of the exporter. If a metric name, type, label or help text
changes, the test fails until this file is written again. Then update the dashboards and the
alert rules that use the metric.

`)
	writeSection(t, &b, base, "/metrics/prusalink", "PrusaLink",
		"One request to PrusaLink for each printer at each scrape. The labels `printer_address`, `printer_model` "+
			"and `printer_name` are the default of `prusalink.common_labels` in `prusa.yml`.", nil)
	writeSection(t, &b, base, "/metrics/udp", "UDP",
		"The fixed series. The firmware metrics are not in this list. A firmware metric `NAME` with a number is "+
			"`prusa_NAME`. An event (`v=T`, or the count `n`) is `prusa_NAME_total`. A string is `prusa_NAME_info` "+
			"with the label `value`. The events in `exporter.udp_event_metrics` start at 0. A field in "+
			"`exporter.udp_counter_fields` is the counter `prusa_NAME_FIELD_total` (`prusa_NAME_total` for `v`).", nil)
	writeSection(t, &b, base, "/metrics", "Exporter",
		"The metrics of the exporter itself. The endpoint also has the standard `go_*`, `process_*` and "+
			"`promhttp_metric_handler_*` metrics, which are not in this list. `prusa_exporter_loki_pushes_total` "+
			"exists only with `--loki.push-url`.",
		func(name string) bool {
			return strings.HasPrefix(name, "go_") || strings.HasPrefix(name, "process_") || strings.HasPrefix(name, "promhttp_")
		})

	got := b.String()
	if *update {
		if err := os.WriteFile(metricsDoc, []byte(got), 0o600); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(metricsDoc)
	if err != nil {
		t.Fatalf("%v. Run the test with -update.", err)
	}
	if string(want) != got {
		t.Errorf("%s is not current. Run: mise exec -- go test -run TestMetricsReference . -args -update\n\ngot:\n%s", metricsDoc, got)
	}
}

func writeSection(t *testing.T, b *strings.Builder, base, path, title, intro string, skip func(string) bool) {
	t.Helper()
	parser := expfmt.NewTextParser(model.UTF8Validation)
	families, err := parser.TextToMetricFamilies(strings.NewReader(get(t, base+path)))
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	names := make([]string, 0, len(families))
	for name := range families {
		if skip == nil || !skip(name) {
			names = append(names, name)
		}
	}
	slices.Sort(names)

	fmt.Fprintf(b, "## %s: `%s`\n\n%s\n\n| Metric | Type | Labels | Description |\n| --- | --- | --- | --- |\n", title, path, intro)
	for _, name := range names {
		mf := families[name]
		fmt.Fprintf(b, "| `%s` | %s | %s | %s |\n", name, strings.ToLower(mf.GetType().String()),
			labelList(mf), strings.ReplaceAll(mf.GetHelp(), "|", `\|`))
	}
	b.WriteString("\n")
}

// labelList returns the label names of all series of the family, without "le".
func labelList(mf *dto.MetricFamily) string {
	var names []string
	for _, m := range mf.GetMetric() {
		for _, l := range m.GetLabel() {
			if l.GetName() != "le" && !slices.Contains(names, l.GetName()) {
				names = append(names, l.GetName())
			}
		}
	}
	slices.Sort(names)
	for i, n := range names {
		names[i] = "`" + n + "`"
	}
	return strings.Join(names, ", ")
}

// TestAlertRuleMetrics makes sure that the alert rules use only metrics of the reference.
func TestAlertRuleMetrics(t *testing.T) {
	t.Parallel()
	rules, err := os.ReadFile("alerts/prusa_exporter.rules.yml")
	if err != nil {
		t.Fatal(err)
	}
	doc, err := os.ReadFile(metricsDoc)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range regexp.MustCompile(`\bprusa_[a-z_]+\b`).FindAllString(string(rules), -1) {
		if name == "prusa_exporter" || name == "prusa_printer" { // the group names
			continue
		}
		if !strings.Contains(string(doc), "`"+name+"`") {
			t.Errorf("the alert rules use %s, which is not in %s", name, metricsDoc)
		}
	}
}
