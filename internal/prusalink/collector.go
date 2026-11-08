package prusalink

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/anyasabo/prusa_exporter/internal/config"
)

// States are the values of printer.state in /api/v1/status (DeviceState in the
// Buddy firmware). prusa_printer_state has one series for each state.
var States = []string{"IDLE", "BUSY", "PRINTING", "PAUSED", "FINISHED", "STOPPED", "ERROR", "ATTENTION", "READY", "UNKNOWN"}

// scrapeTimeoutOffset is the time that the exporter keeps free before the scrape
// timeout of the scraper, to write the response.
const scrapeTimeoutOffset = 500 * time.Millisecond

type metric struct {
	name   string
	help   string
	labels []string
	// common adds the common labels before labels.
	common bool
}

// printerLabels identify a printer on the metrics without the common labels.
var printerLabels = []string{config.LabelAddress, config.LabelModel, config.LabelName}

// The metrics of the collector. The metrics with common=false have fixed printer
// labels, because the scraper must find them also without the job labels.
var (
	mUp = metric{
		"prusa_up", "1 if all PrusaLink requests of the scrape succeeded, else 0.",
		printerLabels,
		false,
	}
	mScrapeDuration = metric{
		"prusa_scrape_duration_seconds", "Duration of the PrusaLink requests of the scrape.",
		printerLabels,
		false,
	}
	mGcodeSent = metric{
		"prusa_udp_metrics_gcode_sent", "1 if the exporter sent and started the G-code that enables the UDP metrics, else 0.",
		printerLabels,
		false,
	}
	mJob = metric{
		"prusa_job_info", "The current print job. The value is always 1. There is no series if there is no job.",
		slices.Concat(printerLabels, []string{config.LabelJobName, config.LabelJobPath}),
		false,
	}

	mInfo = metric{
		"prusa_info", "Information about the printer. The value is always 1.",
		[]string{"api_version", "server_version", "version_text", "prusalink_name", "printer_location", "serial_number", "printer_hostname"},
		true,
	}
	mState = metric{
		"prusa_printer_state", "1 for the current state of the printer, else 0.",
		[]string{"printer_state"},
		true,
	}
	mTemp = metric{
		"prusa_temperature_celsius", "Temperature of a heated element.",
		[]string{"printer_heated_element"},
		true,
	}
	mTempTarget = metric{
		"prusa_temperature_target_celsius", "Target temperature of a heated element.",
		[]string{"printer_heated_element"},
		true,
	}
	mAxis = metric{
		"prusa_axis_position_meters", "Position of an axis.",
		[]string{"printer_axis"},
		true,
	}
	mFan = metric{
		"prusa_fan_speed_rpm", "Speed of a fan in revolutions per minute.",
		[]string{"fan"},
		true,
	}
	mFlow      = metric{"prusa_print_flow_ratio", "Flow factor of the printer, 1 is 100 %.", nil, true}
	mSpeed     = metric{"prusa_print_speed_ratio", "Speed factor of the printer, 1 is 100 %.", nil, true}
	mProgress  = metric{"prusa_printing_progress_ratio", "Progress of the current print, from 0 to 1.", nil, true}
	mPrintTime = metric{"prusa_print_time_seconds", "Time since the start of the current print.", nil, true}
	mRemaining = metric{"prusa_printing_time_remaining_seconds", "Estimated time until the end of the current print.", nil, true}
	mNozzle    = metric{"prusa_nozzle_size_meters", "Diameter of the selected nozzle.", nil, true}
	mMMU       = metric{"prusa_mmu_enabled", "1 if the MMU is enabled, else 0.", nil, true}
	mMaterial  = metric{
		"prusa_material_info", "The loaded filament (\"---\" if none). The value is always 1.",
		[]string{"printer_filament"},
		true,
	}

	allMetrics = []metric{
		mUp, mScrapeDuration, mGcodeSent, mJob, mInfo, mState, mTemp, mTempTarget, mAxis, mFan,
		mFlow, mSpeed, mProgress, mPrintTime, mRemaining, mNozzle, mMMU, mMaterial,
	}
)

// MetricNames returns the names of the PrusaLink metrics.
func MetricNames() []string {
	names := make([]string, len(allMetrics))
	for i, m := range allMetrics {
		names[i] = m.name
	}
	return names
}

// Target is one printer of the exporter.
type Target struct {
	Client *Client
	// GcodeSent is true after the G-code that enables the UDP metrics ran.
	GcodeSent atomic.Bool
	loki      lokiState
}

// Options are the settings of an Exporter.
type Options struct {
	CommonLabels   []string
	DisableMetrics []string
	// Budget is the maximum time of the requests for one printer in one scrape.
	Budget time.Duration
	// Loki pushes the job thumbnail to Loki. It can be nil.
	Loki *LokiPusher
}

// Exporter serves the PrusaLink metrics of all printers. Each scrape sends new requests
// to the printers.
type Exporter struct {
	targets  []*Target
	opts     Options
	descs    map[string]*prometheus.Desc
	disabled map[string]bool
	logger   *slog.Logger
	// errorReg receives the error counter of the metrics handler.
	errorReg prometheus.Registerer
}

// NewExporter returns an exporter for the targets. reg receives the error counter of
// the metrics handler.
func NewExporter(targets []*Target, opts Options, logger *slog.Logger, reg prometheus.Registerer) *Exporter {
	if len(opts.CommonLabels) == 0 {
		opts.CommonLabels = config.DefaultCommonLabels
	}
	e := &Exporter{
		targets:  targets,
		opts:     opts,
		descs:    make(map[string]*prometheus.Desc, len(allMetrics)),
		disabled: make(map[string]bool),
		logger:   logger,
		errorReg: reg,
	}
	for _, m := range allMetrics {
		labels := m.labels
		if m.common {
			labels = append(slices.Clone(opts.CommonLabels), m.labels...)
		}
		e.descs[m.name] = prometheus.NewDesc(m.name, m.help, labels, nil)
	}
	for _, name := range opts.DisableMetrics {
		e.disabled[name] = true
	}
	return e
}

// Handler returns the HTTP handler of the PrusaLink metrics. The scrape ends before
// the scrape timeout of the scraper (header X-Prometheus-Scrape-Timeout-Seconds).
func (e *Exporter) Handler() http.Handler {
	errLog := &countingErrorLog{
		logger: e.logger,
		errors: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "prusa_exporter_prusalink_handler_errors_total",
			Help: "Number of errors of the PrusaLink metrics handler while it gathered or encoded metrics.",
		}),
	}
	e.errorReg.MustRegister(errLog.errors)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		budget := scrapeBudget(e.opts.Budget, r.Header.Get("X-Prometheus-Scrape-Timeout-Seconds"))
		// A new registry for each scrape gives the collector the context of the request.
		reg := prometheus.NewRegistry()
		reg.MustRegister(&scrape{ctx: r.Context(), budget: budget, e: e})
		promhttp.HandlerFor(reg, promhttp.HandlerOpts{
			ErrorLog:      errLog,
			ErrorHandling: promhttp.ContinueOnError,
		}).ServeHTTP(w, r)
	})
}

// countingErrorLog is a promhttp.Logger that logs and counts the errors.
type countingErrorLog struct {
	logger *slog.Logger
	errors prometheus.Counter
}

func (l *countingErrorLog) Println(v ...any) {
	l.errors.Inc()
	l.logger.Error("PrusaLink metrics handler", "err", fmt.Sprint(v...))
}

// scrapeBudget returns the smaller of budget and the scrape timeout in header, minus
// scrapeTimeoutOffset.
func scrapeBudget(budget time.Duration, header string) time.Duration {
	s, err := strconv.ParseFloat(header, 64)
	if err != nil || s <= 0 {
		return budget
	}
	t := time.Duration(s*float64(time.Second)) - scrapeTimeoutOffset
	if t > 0 && t < budget {
		return t
	}
	return budget
}

// scrape is the collector of one scrape.
type scrape struct {
	ctx    context.Context
	budget time.Duration
	e      *Exporter
}

func (s *scrape) Describe(ch chan<- *prometheus.Desc) {
	for _, d := range s.e.descs {
		ch <- d
	}
}

func (s *scrape) Collect(ch chan<- prometheus.Metric) {
	var wg sync.WaitGroup
	for _, t := range s.e.targets {
		wg.Go(func() {
			ctx, cancel := context.WithTimeout(s.ctx, s.budget)
			defer cancel()
			s.e.collectTarget(ctx, t, ch)
		})
	}
	wg.Wait()
}

// emitter sends the metrics of one printer.
type emitter struct {
	e      *Exporter
	ch     chan<- prometheus.Metric
	common []string
}

func (em emitter) send(m metric, value float64, labels ...string) {
	if em.e.disabled[m.name] {
		return
	}
	if m.common {
		labels = append(slices.Clone(em.common), labels...)
	}
	desc := em.e.descs[m.name]
	em.ch <- prometheus.MustNewConstMetric(desc, prometheus.GaugeValue, value, labels...) //nolint:promlinter // The tests lint the output.
}

// snapshot is the data of one printer in one scrape.
type snapshot struct {
	status   Status
	info     Info
	version  Version
	job      Job
	material string
}

func (e *Exporter) fetch(ctx context.Context, c *Client) (snapshot, error) {
	var s snapshot
	var err error
	if s.status, err = c.Status(ctx); err != nil {
		return s, err
	}
	if s.info, err = c.Info(ctx); err != nil {
		return s, err
	}
	if s.version, err = c.Version(ctx); err != nil {
		return s, err
	}
	p, err := c.PrinterState(ctx)
	if err != nil {
		return s, err
	}
	s.material = p.Telemetry.Material
	if s.status.Job != nil {
		if s.job, err = c.Job(ctx); err != nil {
			return s, err
		}
	}
	return s, nil
}

func (e *Exporter) collectTarget(ctx context.Context, t *Target, ch chan<- prometheus.Metric) {
	p := t.Client.Printer
	logger := e.logger.With(config.LabelAddress, p.Address, config.LabelName, p.Name)
	fixed := []string{p.Address, p.Type, p.Name}
	em := emitter{e: e, ch: ch}

	em.send(mGcodeSent, boolToFloat(t.GcodeSent.Load()), fixed...)

	start := time.Now()
	s, err := e.fetch(ctx, t.Client)
	em.send(mScrapeDuration, time.Since(start).Seconds(), fixed...)
	if err != nil {
		logger.Error("PrusaLink scrape failed", "err", err)
		em.send(mUp, 0, fixed...)
		return
	}
	em.send(mUp, 1, fixed...)

	file := s.job.Job.File
	em.common = commonValues(e.opts.CommonLabels, p, file.Name, file.Path)

	if file.Name != "" {
		em.send(mJob, 1, p.Address, p.Type, p.Name, file.Name, file.Path)
		if e.opts.Loki != nil && s.status.Printer.State == "PRINTING" {
			e.opts.Loki.MaybePush(ctx, t, file.Name, file.Path, logger)
		}
	}

	em.send(mInfo, 1, s.version.API, s.version.Server, s.version.Text,
		s.info.Name, s.info.Location, s.info.Serial, s.info.Hostname)

	state := s.status.Printer.State
	for _, st := range States {
		em.send(mState, boolToFloat(st == state), st)
	}
	if state != "" && !slices.Contains(States, state) {
		em.send(mState, 1, state)
	}

	pr := s.status.Printer
	em.send(mTemp, pr.TempBed, "bed")
	em.send(mTemp, pr.TempNozzle, "tool0")
	em.send(mTempTarget, pr.TargetBed, "bed")
	em.send(mTempTarget, pr.TargetNozzle, "tool0")
	// PrusaLink gives millimeters.
	em.send(mAxis, pr.AxisX/1000, "x")
	em.send(mAxis, pr.AxisY/1000, "y")
	em.send(mAxis, pr.AxisZ/1000, "z")
	em.send(mFan, pr.FanHotend, "hotend")
	em.send(mFan, pr.FanPrint, "print")
	// PrusaLink gives percent.
	em.send(mFlow, pr.Flow/100)
	em.send(mSpeed, pr.Speed/100)

	var progress, printTime, remaining float64
	if j := s.status.Job; j != nil {
		progress, printTime, remaining = j.Progress/100, j.TimePrinting, j.TimeRemaining
	}
	em.send(mProgress, progress)
	em.send(mPrintTime, printTime)
	em.send(mRemaining, remaining)

	em.send(mNozzle, s.info.NozzleDiameter/1000)
	em.send(mMMU, boolToFloat(s.info.MMU))
	if s.material != "" {
		em.send(mMaterial, 1, s.material)
	}
}

func commonValues(names []string, p config.Printer, jobName, jobPath string) []string {
	values := make([]string, len(names))
	for i, l := range names {
		switch l {
		case config.LabelAddress:
			values[i] = p.Address
		case config.LabelModel:
			values[i] = p.Type
		case config.LabelName:
			values[i] = p.Name
		case config.LabelJobName:
			values[i] = jobName
		case config.LabelJobPath:
			values[i] = jobPath
		}
	}
	return values
}

func boolToFloat(b bool) float64 {
	if b {
		return 1
	}
	return 0
}
