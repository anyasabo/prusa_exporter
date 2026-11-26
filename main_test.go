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
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/alecthomas/kong"
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
		{"repeated extra metrics", []string{"--udp.extra-metrics=a", "--udp.extra-metrics=b"}, false},
		// With the prefix, each firmware name must be a valid metric name ("24VVoltage").
		{"prefix with a dash", []string{"--udp.prefix=prusa-"}, true},
		{"empty prefix", []string{"--udp.prefix="}, true},
		{"other prefix", []string{"--udp.prefix=printer_"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := parseFlags(tt.args)
			if (err != nil) != tt.isErr {
				t.Errorf("parseFlags(%v) error = %v, want error %v", tt.args, err, tt.isErr)
			}
		})
	}
}

// TestParseFlagValues records the value of each flag, so that a change of the flag
// library keeps the names, the defaults and the "--no-" form of the boolean flags.
func TestParseFlagValues(t *testing.T) {
	t.Parallel()
	type values struct {
		configFile, telemetryPath, prusaLinkPath, udpPath, udpListen, udpPrefix string
		gcodeEnabled                                                            bool
		gcodeHost                                                               string
		gcodeAll                                                                bool
		gcodeExtra                                                              []string
		lokiPushURL                                                             string
		enablePprof                                                             bool
		listen                                                                  []string
		systemdSocket                                                           bool
		webConfigFile, logLevel, logFormat                                      string
	}
	get := func(f *flags) values {
		return values{
			f.configFile, f.telemetryPath, f.prusaLinkPath, f.udpPath, f.udpListen, f.udpPrefix,
			f.gcodeEnabled, f.gcodeHost, f.gcodeAll, f.gcodeExtra, f.lokiPushURL, f.enablePprof,
			*f.webFlags.WebListenAddresses, *f.webFlags.WebSystemdSocket, *f.webFlags.WebConfigFile,
			f.promslogConfig.Level.String(), f.promslogConfig.Format.String(),
		}
	}
	defaults := values{
		configFile: "prusa.yml", telemetryPath: "/metrics", prusaLinkPath: "/metrics/prusalink",
		udpPath: "/metrics/udp", udpListen: ":8514", udpPrefix: "prusa_", gcodeEnabled: true,
		listen: []string{":10009"}, logLevel: "info", logFormat: "logfmt",
	}
	all := values{
		configFile: "c.yml", telemetryPath: "/t", prusaLinkPath: "/p", udpPath: "/u",
		udpListen: ":1", udpPrefix: "x_", gcodeEnabled: false, gcodeHost: "10.0.0.1", gcodeAll: true,
		gcodeExtra: []string{"a", "b"}, lokiPushURL: "http://loki/push", enablePprof: true,
		listen: []string{":1", ":2"}, systemdSocket: true, webConfigFile: "w.yml", logLevel: "debug", logFormat: "json",
	}
	tests := []struct {
		name string
		args []string
		want values
	}{
		{"defaults", nil, defaults},
		{"all", []string{
			"--config.file=c.yml", "--web.telemetry-path=/t", "--exporter.metrics-path=/p",
			"--exporter.udp-metrics-path=/u", "--udp.listen-address=:1", "--udp.prefix=x_",
			"--no-udp.gcode-enabled", "--udp.ip-override=10.0.0.1", "--udp.all-metrics",
			"--udp.extra-metrics=a", "--udp.extra-metrics", "b", "--loki.push-url=http://loki/push",
			"--web.enable-pprof", "--web.listen-address=:1", "--web.listen-address=:2",
			"--web.systemd-socket", "--web.config.file=w.yml", "--log.level=debug", "--log.format=json",
		}, all},
		{"gcode enabled", []string{"--udp.gcode-enabled"}, defaults},
		// kingpin accepted "--no-" for each boolean flag.
		{"no booleans", []string{"--no-udp.all-metrics", "--no-web.enable-pprof", "--no-web.systemd-socket"}, defaults},
		// promslog.Level.Set accepts upper case. promslog.Format.Set does not.
		{"upper-case log level", []string{"--log.level=INFO"}, defaults},
		// A comma is part of the value: there is no list separator.
		{"comma in a repeatable flag", []string{"--udp.extra-metrics=a,b", "--web.listen-address=:1,:2"}, func() values {
			v := defaults
			v.gcodeExtra, v.listen = []string{"a,b"}, []string{":1,:2"}
			return v
		}()},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			f, err := parseFlags(tt.args)
			if err != nil {
				t.Fatal(err)
			}
			if got := get(f); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("parseFlags(%q):\ngot  %+v\nwant %+v", tt.args, got, tt.want)
			}
		})
	}
	for _, args := range [][]string{{"--log.level=trace"}, {"--log.format=xml"}, {"--udp.gcode-enabled=maybe"}} {
		if _, err := parseFlags(args); err == nil {
			t.Errorf("parseFlags(%q): no error", args)
		}
	}
}

// TestHelpAndVersion checks that --help and --version print to stdout and exit with 0.
func TestHelpAndVersion(t *testing.T) {
	t.Parallel()
	for _, arg := range []string{"--help", "-h", "--version"} {
		var out strings.Builder
		code := -1
		func() {
			// kong calls Exit and does not expect it to return. The panic stops the parse.
			defer func() { _ = recover() }()
			_, _ = parseFlags([]string{arg}, kong.Writers(&out, io.Discard), kong.Exit(func(c int) { code = c; panic(arg) }))
		}()
		if code != 0 || !strings.Contains(out.String(), name) {
			t.Errorf("%s: exit code %d, output %q", arg, code, out.String())
		}
	}
}

func freePort(t *testing.T, network string) string {
	t.Helper()
	switch network {
	case "udp":
		c, err := (&net.ListenConfig{}).ListenPacket(t.Context(), "udp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		return c.LocalAddr().String()
	default:
		l, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
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
	t.Parallel()
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

	cfg := "printers:\n  - address: " + strings.TrimPrefix(printer.URL, "http://") + "\n    apikey: k\n    name: P\n    type: MK4\n"
	base, udpAddr, stop := startExporter(t, cfg, "--web.enable-pprof")

	conn, err := (&net.Dialer{}).DialContext(t.Context(), "udp", udpAddr)
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
		// The earlier GET /metrics requests of this list are in the histogram.
		{"/metrics", `prusa_exporter_http_request_duration_seconds_count{code="200",handler="metrics"}`},
		{"/", "Exporter metrics"},
		{"/debug/pprof/", "goroutine"},
	} {
		if body := get(t, base+check.path); !strings.Contains(body, check.want) {
			t.Errorf("GET %s has no %q:\n%s", check.path, check.want, body)
		}
	}
	deadline := time.Now().Add(5 * time.Second)
	for !strings.Contains(get(t, base+"/metrics/udp"), `prusa_temp_noz{printer_address="127.0.0.1",printer_mac="AA:BB"} 200`) {
		if time.Now().After(deadline) {
			t.Fatal("no UDP metric")
		}
		time.Sleep(20 * time.Millisecond)
	}

	if err := stop(); err != nil {
		t.Errorf("run() = %v after cancel, want nil", err)
	}
}

func TestRunBadConfig(t *testing.T) {
	t.Parallel()
	f, err := parseFlags([]string{"--config.file=" + filepath.Join(t.TempDir(), "missing.yml")})
	if err != nil {
		t.Fatal(err)
	}
	if err := run(t.Context(), f, slog.New(slog.DiscardHandler)); err == nil {
		t.Error("run() with a missing config file: no error")
	}
}

// startExporter runs the exporter with the config text and the extra flags, and waits
// until it answers. It returns the HTTP base URL, the UDP address and a function that
// stops the exporter and returns the error of run().
func startExporter(t *testing.T, cfg string, args ...string) (base, udpAddr string, stop func() error) {
	t.Helper()
	cfgFile := filepath.Join(t.TempDir(), "prusa.yml")
	if err := os.WriteFile(cfgFile, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	httpAddr := freePort(t, "tcp")
	udpAddr = freePort(t, "udp")
	f, err := parseFlags(append([]string{
		"--config.file=" + cfgFile,
		"--web.listen-address=" + httpAddr,
		"--udp.listen-address=" + udpAddr,
		"--no-udp.gcode-enabled",
	}, args...))
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- run(ctx, f, slog.New(slog.DiscardHandler)) }()

	base = "http://" + httpAddr
	deadline := time.Now().Add(10 * time.Second)
	for {
		req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, base+"/", nil)
		if res, err := http.DefaultClient.Do(req); err == nil {
			_ = res.Body.Close()
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the exporter did not start")
		}
		time.Sleep(20 * time.Millisecond)
	}
	stop = func() error {
		cancel()
		select {
		case err := <-done:
			return err
		case <-time.After(15 * time.Second):
			t.Fatal("run() did not return after cancel")
			return nil
		}
	}
	return base, udpAddr, stop
}
