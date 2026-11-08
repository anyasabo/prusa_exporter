package logging

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/prometheus/common/promslog"
)

func newConfig(t *testing.T, format, level string) *promslog.Config {
	t.Helper()
	cfg := &promslog.Config{Format: promslog.NewFormat(), Level: promslog.NewLevel()}
	if err := cfg.Format.Set(format); err != nil {
		t.Fatal(err)
	}
	if err := cfg.Level.Set(level); err != nil {
		t.Fatal(err)
	}
	return cfg
}

// keys returns the top-level keys of a JSON object in their order.
func keys(t *testing.T, line []byte) []string {
	t.Helper()
	dec := json.NewDecoder(bytes.NewReader(line))
	if _, err := dec.Token(); err != nil {
		t.Fatal(err)
	}
	var r []string
	for dec.More() {
		k, err := dec.Token()
		if err != nil {
			t.Fatal(err)
		}
		r = append(r, k.(string))
		var v json.RawMessage
		if err := dec.Decode(&v); err != nil {
			t.Fatal(err)
		}
	}
	return r
}

func TestECSJSON(t *testing.T) {
	t.Parallel()
	var b bytes.Buffer
	logger := New(newConfig(t, "json", "info"), &b, "prusa_exporter", "1.2.3")
	logger.With(PrinterAddress, "10.0.0.2").Warn("Something failed", ErrorMessage, errors.New("boom"))
	logger.Debug("Not written")

	lines := bytes.Split(bytes.TrimSpace(b.Bytes()), []byte("\n"))
	if len(lines) != 1 {
		t.Fatalf("%d lines, want 1:\n%s", len(lines), b.String())
	}
	got := keys(t, lines[0])
	// The ECS logging spec wants @timestamp, log.level and message as the first fields.
	if len(got) < 3 || got[0] != "@timestamp" || got[1] != "log.level" || got[2] != "message" {
		t.Errorf("first keys = %v, want @timestamp, log.level, message", got)
	}

	var m map[string]any
	if err := json.Unmarshal(lines[0], &m); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		"log.level":             "warn",
		"message":               "Something failed",
		"ecs.version":           ECSVersion,
		"service.name":          "prusa_exporter",
		"service.version":       "1.2.3",
		"error.message":         "boom",
		"prusa.printer.address": "10.0.0.2",
		"log.origin.file.name":  "logging_test.go",
	}
	for k, v := range want {
		if m[k] != v {
			t.Errorf("%s = %v, want %v", k, m[k], v)
		}
	}
	if ts, _ := m["@timestamp"].(string); !strings.HasSuffix(ts, "Z") {
		t.Errorf("@timestamp = %q, want UTC", ts)
	}
	if line, _ := m["log.origin.file.line"].(float64); line == 0 {
		t.Error("no log.origin.file.line")
	}
	for _, k := range []string{"time", "level", "msg", "source"} {
		if _, ok := m[k]; ok {
			t.Errorf("the slog key %q is in the output", k)
		}
	}
}

func TestLibraryKeys(t *testing.T) {
	t.Parallel()
	var b bytes.Buffer
	// The same keys as exporter-toolkit. Raw keys are necessary here.
	New(newConfig(t, "json", "info"), &b, "s", "v").Info("TLS is disabled.", "http2", false, "address", "[::]:10009", "err", "x") //nolint:sloglint // The test uses the raw keys of the library.
	var m map[string]any
	if err := json.Unmarshal(b.Bytes(), &m); err != nil {
		t.Fatal(err)
	}
	if m[ServerAddress] != "[::]:10009" || m[WebHTTP2] != false || m[ErrorMessage] != "x" {
		t.Errorf("library keys are not mapped: %v", m)
	}
}

func TestLogfmt(t *testing.T) {
	t.Parallel()
	var b bytes.Buffer
	New(newConfig(t, "logfmt", "debug"), &b, "prusa_exporter", "1.2.3").Debug("Hello", PrinterName, "P")
	if s := b.String(); !strings.Contains(s, "level=DEBUG") || !strings.Contains(s, "prusa.printer.name=P") {
		t.Errorf("logfmt line = %q", s)
	}
}

// TestKeys makes sure that each key is an ECS field or in the custom namespace "prusa".
// A key that is a prefix of an ECS object (for example "error") breaks the mapping in
// Elasticsearch.
func TestKeys(t *testing.T) {
	t.Parallel()
	// These keys are ECS fields with a type that takes the logged value. Before you add
	// a key here, find it in https://www.elastic.co/docs/reference/ecs.
	ecs := map[string]bool{ErrorMessage: true, ServerAddress: true, SourceIP: true, SourcePort: true}
	custom := []string{
		PrinterAddress, PrinterName, PrinterMAC, MetricName, MetricLabels, PointError, PointLine,
		SeriesLimit, ConfigKey, RetryIn, BuildInfo, BuildContext, WebHTTP2, RateBurst, RateInterval,
	}
	for from, to := range libraryKeys {
		if !ecs[to] && !strings.HasPrefix(to, "prusa.") {
			t.Errorf("library key %s maps to %s, which is not in the ECS list or the namespace prusa", from, to)
		}
	}
	for k := range ecs {
		if strings.HasPrefix(k, "prusa.") {
			t.Errorf("%s is in the ECS list but has the custom prefix", k)
		}
	}
	for _, k := range custom {
		if !strings.HasPrefix(k, "prusa.") {
			t.Errorf("custom key %s is not in the namespace prusa", k)
		}
	}
}
