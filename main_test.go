package main

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alecthomas/kingpin/v2"
)

func TestParseFlags(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		args  []string
		isErr bool
	}{
		{"defaults", nil, false},
		{"same paths", []string{"--exporter.metrics-path=/metrics"}, true},
		{"root path", []string{"--exporter.udp-metrics-path=/"}, true},
		{"unknown flag", []string{"--exporter.metrics-port=1"}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			app := kingpin.New(name, "")
			app.Terminate(nil)
			app.UsageWriter(io.Discard)
			app.ErrorWriter(io.Discard)
			_, err := parseFlags(app, tt.args)
			if (err != nil) != tt.isErr {
				t.Errorf("parseFlags(%v) error = %v, want error %v", tt.args, err, tt.isErr)
			}
		})
	}
}

func TestSplitList(t *testing.T) {
	t.Parallel()
	got := splitList(" a, b,,c ")
	if strings.Join(got, "|") != "a|b|c" {
		t.Errorf("splitList() = %q", got)
	}
	if splitList("") != nil {
		t.Error("splitList(\"\") is not nil")
	}
}

func freePort(t *testing.T, network string) string {
	t.Helper()
	switch network {
	case "udp":
		c, err := net.ListenPacket("udp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		return c.LocalAddr().String()
	default:
		l, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		defer l.Close()
		return l.Addr().String()
	}
}

func get(t *testing.T, url string) string {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != http.StatusOK {
		t.Fatalf("GET %s: %s", url, res.Status)
	}
	return string(body)
}

// TestRun starts the exporter with a fake printer, sends a UDP message, reads all
// endpoints and stops the exporter.
func TestRun(t *testing.T) {
	printer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/status":
			_, _ = io.WriteString(w, `{"printer":{"state":"IDLE","temp_bed":22.5}}`)
		case "/api/v1/info", "/api/version", "/api/printer":
			_, _ = io.WriteString(w, `{}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer printer.Close()

	dir := t.TempDir()
	cfgFile := filepath.Join(dir, "prusa.yml")
	cfg := "printers:\n  - address: " + strings.TrimPrefix(printer.URL, "http://") + "\n    apikey: k\n    name: P\n    type: MK4\n"
	if err := os.WriteFile(cfgFile, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	httpAddr, udpAddr := freePort(t, "tcp"), freePort(t, "udp")
	app := kingpin.New(name, "")
	f, err := parseFlags(app, []string{
		"--config.file=" + cfgFile,
		"--web.listen-address=" + httpAddr,
		"--udp.listen-address=" + udpAddr,
		"--no-udp.gcode-enabled",
	})
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- run(ctx, f, slog.New(slog.DiscardHandler)) }()

	base := "http://" + httpAddr
	deadline := time.Now().Add(10 * time.Second)
	for {
		req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, base+"/", nil)
		if res, err := http.DefaultClient.Do(req); err == nil {
			res.Body.Close()
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the exporter did not start")
		}
		time.Sleep(20 * time.Millisecond)
	}

	conn, err := net.Dial("udp", udpAddr)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.Write([]byte("<14>1 - AA:BB buddy - - - msg=1,tm=1,v=4 temp_noz v=200 0\n")); err != nil {
		t.Fatal(err)
	}

	for _, check := range []struct{ path, want string }{
		{"/metrics/prusalink", `prusa_temperature_celsius{printer_address="` + strings.TrimPrefix(printer.URL, "http://") + `",printer_heated_element="bed",printer_model="MK4",printer_name="P"} 22.5`},
		{"/metrics/prusalink", `prusa_printer_state{`},
		{"/metrics", "prusa_exporter_build_info{"},
		{"/metrics", "go_goroutines "},
		{"/metrics", `prusa_exporter_prusalink_request_duration_seconds_count{code="200",endpoint="/api/v1/status"`},
		{"/", "Exporter metrics"},
	} {
		if body := get(t, base+check.path); !strings.Contains(body, check.want) {
			t.Errorf("GET %s has no %q:\n%s", check.path, check.want, body)
		}
	}
	deadline = time.Now().Add(5 * time.Second)
	for !strings.Contains(get(t, base+"/metrics/udp"), `prusa_temp_noz{printer_address="127.0.0.1",printer_mac="AA:BB"} 200`) {
		if time.Now().After(deadline) {
			t.Fatal("no UDP metric")
		}
		time.Sleep(20 * time.Millisecond)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("run() = %v after cancel, want nil", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("run() did not return after cancel")
	}
}

func TestRunBadConfig(t *testing.T) {
	t.Parallel()
	app := kingpin.New(name, "")
	f, err := parseFlags(app, []string{"--config.file=" + filepath.Join(t.TempDir(), "missing.yml")})
	if err != nil {
		t.Fatal(err)
	}
	if err := run(t.Context(), f, slog.New(slog.DiscardHandler)); err == nil {
		t.Error("run() with a missing config file: no error")
	}
}
