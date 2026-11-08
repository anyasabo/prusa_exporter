package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestParse(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		yaml    string
		want    *Config
		wantErr string
	}{
		{
			name: "defaults",
			yaml: "printers:\n  - address: 10.0.0.1\n    apikey: k\n",
			want: &Config{
				Exporter: Exporter{
					UDPSeriesTTL:          DefaultUDPSeriesTTL,
					UDPMaxSeries:          DefaultUDPMaxSeries,
					PrusaLinkScrapeBudget: DefaultPrusaLinkScrapeBudget,
				},
				Printers:  []Printer{{Address: "10.0.0.1", APIKey: "k"}},
				PrusaLink: PrusaLink{CommonLabels: DefaultCommonLabels},
			},
		},
		{
			name: "all keys",
			yaml: `
exporter:
  udp_series_ttl: 90s
  udp_sample_timestamps: true
  udp_max_series: 50
  prusalink_scrape_budget: 3s
printers:
  - address: printer.lan:8080
    username: maker
    password: secret
    name: MK4
    type: MK4S
prusalink:
  common_labels: [printer_name, printer_job_name]
  disable_metrics: [prusa_info]
`,
			want: &Config{
				Exporter: Exporter{
					UDPSeriesTTL:          90 * time.Second,
					UDPSampleTimestamps:   true,
					UDPMaxSeries:          50,
					PrusaLinkScrapeBudget: 3 * time.Second,
				},
				Printers: []Printer{{Address: "printer.lan:8080", Username: "maker", Password: "secret", Name: "MK4", Type: "MK4S"}},
				PrusaLink: PrusaLink{
					CommonLabels:   []string{"printer_name", "printer_job_name"},
					DisableMetrics: []string{"prusa_info"},
				},
			},
		},
		{
			name: "empty file",
			yaml: "",
			want: &Config{
				Exporter: Exporter{
					UDPSeriesTTL:          DefaultUDPSeriesTTL,
					UDPMaxSeries:          DefaultUDPMaxSeries,
					PrusaLinkScrapeBudget: DefaultPrusaLinkScrapeBudget,
				},
				PrusaLink: PrusaLink{CommonLabels: DefaultCommonLabels},
			},
		},
		{name: "unknown key", yaml: "exporter:\n  udp_series_tll: 1m\n", wantErr: "field udp_series_tll not found"},
		{name: "bad duration", yaml: "exporter:\n  udp_series_ttl: soon\n", wantErr: "decode config"},
		{name: "no address", yaml: "printers:\n  - apikey: k\n", wantErr: "printers[0]: address is empty"},
		{name: "duplicate address", yaml: "printers:\n  - {address: a, apikey: k}\n  - {address: a, apikey: k}\n", wantErr: "printers[1]: address \"a\" is a duplicate"},
		{name: "no credentials", yaml: "printers:\n  - address: a\n", wantErr: "set apikey, or username and password"},
		{name: "unknown label", yaml: "prusalink:\n  common_labels: [printer_serial]\n", wantErr: "unknown label \"printer_serial\""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := Parse([]byte(tt.yaml))
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("Parse() error = %v, want an error with %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Parse() error = %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Parse() =\n%+v\nwant\n%+v", got, tt.want)
			}
		})
	}
}

func TestDeprecated(t *testing.T) {
	t.Parallel()
	c, err := Parse([]byte("exporter:\n  scrape_timeout: 5\n  log_level: debug\n"))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := c.Deprecated(), []string{"exporter.scrape_timeout", "exporter.log_level"}; !reflect.DeepEqual(got, want) {
		t.Errorf("Deprecated() = %v, want %v", got, want)
	}
}

func TestLoad(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "prusa.yml")
	if err := os.WriteFile(path, []byte("printers:\n  - {address: a, apikey: k}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Printers) != 1 {
		t.Errorf("Load() has %d printers, want 1", len(c.Printers))
	}
	if _, err := Load(filepath.Join(t.TempDir(), "missing.yml")); err == nil {
		t.Error("Load() of a missing file: no error")
	}
}

func TestDefaultCommonLabelsAreNotShared(t *testing.T) {
	t.Parallel()
	c, err := Parse(nil)
	if err != nil {
		t.Fatal(err)
	}
	c.PrusaLink.CommonLabels[0] = "changed"
	if DefaultCommonLabels[0] == "changed" {
		t.Error("Parse() returned the DefaultCommonLabels slice, not a copy")
	}
}
