package udp

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/anyasabo/prusa_exporter/v3/internal/prusalink"
)

// TestFirmwareMetricNames sends one point for each metric name of the firmware lists.
// Each name must make a series: a check of the names must not drop a real metric.
func TestFirmwareMetricNames(t *testing.T) {
	t.Parallel()
	c, _ := newTestCollector(t, Options{})
	for _, name := range prusalink.AllUDPMetrics {
		c.Handle(t.Context(), msg(name+" v=1 0"), printer, t0)
	}
	if got := testutil.ToFloat64(c.metrics.pointsDropped); got != 0 {
		t.Errorf("dropped points = %v, want 0", got)
	}
	// Each name makes one gauge, plus the printer metrics.
	if got, want := testutil.CollectAndCount(c), len(prusalink.AllUDPMetrics)+4; got != want {
		t.Errorf("series = %d, want %d", got, want)
	}
}
