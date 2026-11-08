package prusalink

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestGcode(t *testing.T) {
	t.Parallel()
	header := "M330 SYSLOG\nM334 10.0.0.2 8514\nM340 10.0.0.2 13514\n"
	tests := []struct {
		name        string
		opts        GcodeOptions
		enable, dis int
		contains    []string
		notContains []string
	}{
		{"default", GcodeOptions{Port: 8514}, len(DefaultUDPMetrics), len(AllUDPMetrics), []string{"M331 temp_noz"}, []string{"M331 stp_stall"}},
		{"extra", GcodeOptions{Port: 8514, Extra: []string{"stp_stall"}}, len(DefaultUDPMetrics) + 1, len(AllUDPMetrics), []string{"M331 stp_stall"}, nil},
		{"all", GcodeOptions{Port: 8514, All: true}, len(AllUDPMetrics), 0, []string{"M331 stp_stall"}, []string{"M332"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			g := tt.opts.Gcode("10.0.0.2")
			if !strings.HasPrefix(g, header) {
				t.Errorf("G-code does not start with the host lines:\n%s", g[:min(len(g), 100)])
			}
			if n := strings.Count(g, "\nM331 "); n != tt.enable {
				t.Errorf("%d M331 lines, want %d", n, tt.enable)
			}
			if n := strings.Count(g, "\nM332 "); n != tt.dis {
				t.Errorf("%d M332 lines, want %d", n, tt.dis)
			}
			for _, s := range tt.contains {
				if !strings.Contains(g, s+"\n") && !strings.HasSuffix(g, s) {
					t.Errorf("G-code has no %q", s)
				}
			}
			for _, s := range tt.notContains {
				if strings.Contains(g, s) {
					t.Errorf("G-code has %q", s)
				}
			}
		})
	}
}

func TestGcodeDoesNotChangeDefaults(t *testing.T) {
	t.Parallel()
	n := len(DefaultUDPMetrics)
	for range 3 {
		GcodeOptions{Extra: []string{"x"}}.Gcode("h")
	}
	if len(DefaultUDPMetrics) != n {
		t.Errorf("Gcode() changed DefaultUDPMetrics: %d entries, want %d", len(DefaultUDPMetrics), n)
	}
}

func TestEnableUDPMetrics(t *testing.T) {
	t.Parallel()
	for _, digest := range []bool{true, false} {
		f := newFakePrusaLink(t, digest)
		c := NewClient(f.printer(), NewTransport(), nil)
		if err := EnableUDPMetrics(t.Context(), c, GcodeOptions{Host: "10.0.0.2", Port: 9999}); err != nil {
			t.Fatalf("digest=%v: EnableUDPMetrics() = %v", digest, err)
		}
		path := "/api/v1/files/usb//" + gcodeFile
		for _, m := range []string{"DELETE", "PUT", "POST"} {
			if n := f.count(m + " " + path); n != 1 {
				t.Errorf("digest=%v: %s sent %d times, want 1", digest, m, n)
			}
		}
		f.mu.Lock()
		body := f.bodies["PUT "+path]
		f.mu.Unlock()
		if !strings.Contains(body, "M334 10.0.0.2 9999\n") {
			t.Errorf("uploaded G-code has no M334 line for the host and port:\n%.100s", body)
		}
	}
}

func TestEnableUDPMetricsLocalAddress(t *testing.T) {
	t.Parallel()
	f := newFakePrusaLink(t, false)
	c := NewClient(f.printer(), NewTransport(), nil)
	if err := EnableUDPMetrics(t.Context(), c, GcodeOptions{Port: 8514}); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if body := f.bodies["PUT /api/v1/files/usb//"+gcodeFile]; !strings.Contains(body, "M334 127.0.0.1 8514") {
		t.Errorf("G-code does not use the local address of the route to the printer:\n%.100s", body)
	}
}

func TestEnableUDPMetricsFails(t *testing.T) {
	t.Parallel()
	for _, method := range []string{"PUT", "POST"} {
		f := newFakePrusaLink(t, false)
		c := NewClient(f.printer(), NewTransport(), nil)
		f.server.Config.Handler = failMethod(method, f)
		if err := EnableUDPMetrics(t.Context(), c, GcodeOptions{Host: "h", Port: 1}); err == nil {
			t.Errorf("EnableUDPMetrics() = nil after a failed %s", method)
		}
	}
}

func failMethod(method string, f *fakePrusaLink) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == method {
			w.WriteHeader(http.StatusConflict)
			return
		}
		f.serve(w, r)
	})
}

func TestRunGcodeRetries(t *testing.T) {
	t.Parallel()
	f := newFakePrusaLink(t, false)
	path := "/api/v1/files/usb//" + gcodeFile
	f.setCode(path, http.StatusServiceUnavailable)
	target := &Target{Client: NewClient(f.printer(), NewTransport(), nil)}
	go func() {
		time.Sleep(100 * time.Millisecond)
		f.mu.Lock()
		delete(f.codes, path)
		f.mu.Unlock()
	}()
	RunGcode(t.Context(), []*Target{target}, GcodeOptions{Host: "h", Port: 1, Timeout: time.Second, RetryInterval: 20 * time.Millisecond}, discardLogger())
	if !target.GcodeSent.Load() {
		t.Error("GcodeSent is false after RunGcode returned")
	}
}

func TestLocalAddress(t *testing.T) {
	t.Parallel()
	for _, addr := range []string{"127.0.0.1", "127.0.0.1:8080"} {
		got, err := localAddress(t.Context(), addr)
		if err != nil || got != "127.0.0.1" {
			t.Errorf("localAddress(%q) = %q, %v, want 127.0.0.1", addr, got, err)
		}
	}
}
