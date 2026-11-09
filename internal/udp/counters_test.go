package udp

import (
	"testing"
	"time"

	dto "github.com/prometheus/client_model/go"
)

var testCounterFields = map[string][]string{
	"stp_stall": {"v"},
	"task_cpu":  {"t"},
	"ps_stalls": {"x", "y"},
}

func newCounterCollector(t *testing.T) *Collector {
	t.Helper()
	c, _ := newTestCollector(t, Options{SeriesTTL: 10 * time.Minute, CounterFields: testCounterFields})
	return c
}

func TestCounterFields(t *testing.T) {
	t.Parallel()
	c := newCounterCollector(t)
	send(t, c,
		"stp_stall v=5i",
		"task_cpu,n=IDLE c=230i,p=0i,t=1000i",
		"ps_stalls x=3i,y=7i",
		"plan_min v=4i",
	)
	wantSamples(t, c, map[string]float64{
		"prusa_stp_stall_total{" + testLabels + "}":             5,
		"prusa_task_cpu_t_total{n=\"IDLE\"," + testLabels + "}": 1000,
		"prusa_task_cpu_c{n=\"IDLE\"," + testLabels + "}":       230,
		"prusa_ps_stalls_x_total{" + testLabels + "}":           3,
		"prusa_ps_stalls_y_total{" + testLabels + "}":           7,
		"prusa_plan_min{" + testLabels + "}":                    4,
	})
	_, types := samples(t, c)
	for name, want := range map[string]dto.MetricType{
		"prusa_stp_stall_total":   dto.MetricType_COUNTER,
		"prusa_task_cpu_t_total":  dto.MetricType_COUNTER,
		"prusa_task_cpu_c":        dto.MetricType_GAUGE,
		"prusa_ps_stalls_x_total": dto.MetricType_COUNTER,
		"prusa_plan_min":          dto.MetricType_GAUGE,
	} {
		if types[name] != want {
			t.Errorf("type of %s = %v, want %v", name, types[name], want)
		}
	}
	for _, old := range []string{"prusa_stp_stall", "prusa_task_cpu_t", "prusa_ps_stalls_x"} {
		if _, ok := types[old]; ok {
			t.Errorf("a counter field also makes the gauge %s", old)
		}
	}
}

// The counter takes the value of the printer. After a printer restart, the value goes
// down, and rate() sees a counter reset.
func TestCounterFieldsRestart(t *testing.T) {
	t.Parallel()
	c := newCounterCollector(t)
	send(t, c, "stp_stall v=500i")
	send(t, c, "stp_stall v=2i")
	wantSamples(t, c, map[string]float64{"prusa_stp_stall_total{" + testLabels + "}": 2})
}

// A counter does not expire, like the event counters.
func TestCounterFieldsDoNotExpire(t *testing.T) {
	t.Parallel()
	c, _ := newTestCollector(t, Options{SeriesTTL: time.Minute, CounterFields: testCounterFields})
	send(t, c, "stp_stall v=5i", "plan_min v=4i")
	c.now = func() time.Time { return t0.Add(2 * time.Minute) }
	got, _ := samples(t, c)
	if _, ok := got["prusa_stp_stall_total{"+testLabels+"}"]; !ok {
		t.Error("the counter expired")
	}
	if _, ok := got["prusa_plan_min{"+testLabels+"}"]; ok {
		t.Error("the gauge did not expire")
	}
}

// Without CounterFields, all numbers are gauges.
func TestNoCounterFields(t *testing.T) {
	t.Parallel()
	c, _ := newTestCollector(t, Options{SeriesTTL: 10 * time.Minute})
	send(t, c, "stp_stall v=5i")
	wantSamples(t, c, map[string]float64{"prusa_stp_stall{" + testLabels + "}": 5})
}
