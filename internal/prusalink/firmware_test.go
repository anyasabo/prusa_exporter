package prusalink

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/anyasabo/prusa_exporter/internal/config"
)

// experimentMetrics are in the reference build of docs/firmware/coreonel.json, but
// only in the experiment branch of the firmware (76918075f), not upstream. Remove this
// list when the reference comes from an upstream build.
var experimentMetrics = []string{
	"heap_x", "http_blk", "net_lat", "rx_ring", "rxd_eth", "rxd_tcp", "task_cpu", "tcpip_blk",
	"upx", "upx2", "upx_f", "upx_g", "upx_n", "upx_t", "upx_u", "upx_v",
	"usb_io", "usb_io2", "usb_io3",
}

type firmwareReference struct {
	Model   string `json:"model"`
	Metrics []struct {
		Name string `json:"name"`
		Type string `json:"type"`
	} `json:"metrics"`
}

// TestFirmwareReference checks the metric lists of the exporter against the metric
// definitions of the firmware builds. Make a reference with internal/tools/fwmetrics.
func TestFirmwareReference(t *testing.T) {
	t.Parallel()
	files, err := filepath.Glob("../../docs/firmware/*.json")
	if err != nil || len(files) == 0 {
		t.Fatalf("no firmware references: %v", err)
	}
	for _, file := range files {
		data, err := os.ReadFile(file) //nolint:gosec // The path comes from the glob above.
		if err != nil {
			t.Fatal(err)
		}
		var ref firmwareReference
		if err := json.Unmarshal(data, &ref); err != nil {
			t.Fatalf("%s: %v", file, err)
		}
		var events []string
		for _, m := range ref.Metrics {
			if m.Type == "event" {
				events = append(events, m.Name)
			}
			if !slices.Contains(AllUDPMetrics, m.Name) && !slices.Contains(experimentMetrics, m.Name) {
				t.Errorf("%s: firmware metric %q is not in AllUDPMetrics. The G-code does not disable it, and --udp.all-metrics does not enable it.", ref.Model, m.Name)
			}
		}
		for _, e := range events {
			if !slices.Contains(config.DefaultUDPEventMetrics, e) {
				t.Errorf("%s: event metric %q is not in config.DefaultUDPEventMetrics", ref.Model, e)
			}
		}
	}
}
