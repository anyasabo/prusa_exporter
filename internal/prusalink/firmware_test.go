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
// only in the fork of the firmware (branches upload-experiments and metrics-defaults),
// not upstream. Remove this list when the reference comes from an upstream build.
var experimentMetrics = []string{
	"gui_min", "gui_prof", "gui_prof2", "gui_prof3", "heap_min", "heap_x", "http_blk",
	"net_cnt", "net_cnt2", "net_lat", "phstep_prof", "phstep_prof2", "phstep_prof3",
	"ps_spd_big", "ps_spd_chgs", "ps_stall_its", "ps_stalls", "rx_ring", "rxd_eth", "rxd_tcp",
	"shaper_fit", "syslog_drop", "task_cpu", "tcpip_blk",
	"upx", "upx2", "upx3", "upx_c", "upx_f", "upx_g", "upx_n", "upx_t", "upx_u", "upx_v",
	"usb_cnt", "usb_cnt2", "usb_cnt3", "usb_io", "usb_io2", "usb_io3",
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
		checkCounterFields(t, ref)
	}
}

// checkCounterFields checks that each metric of config.DefaultUDPCounterFields is in
// the firmware build, and that its type can carry the fields: "v" needs an integer or
// a float metric, and named fields need a custom metric. The fields themselves are in
// the format strings of the firmware, which the ELF does not keep.
func checkCounterFields(t *testing.T, ref firmwareReference) {
	t.Helper()
	types := make(map[string]string, len(ref.Metrics))
	for _, m := range ref.Metrics {
		types[m.Name] = m.Type
	}
	for name, fields := range config.DefaultUDPCounterFields {
		typ, ok := types[name]
		if !ok {
			t.Errorf("%s: counter metric %q of config.DefaultUDPCounterFields is not in the firmware", ref.Model, name)
			continue
		}
		for _, f := range fields {
			switch {
			case f == "v" && typ != "integer" && typ != "float":
				t.Errorf("%s: counter metric %q has the field v, but its type is %s", ref.Model, name, typ)
			case f != "v" && typ != "custom":
				t.Errorf("%s: counter metric %q has the field %q, but its type is %s, not custom", ref.Model, name, f, typ)
			}
		}
	}
}
