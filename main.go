// Command prusa_exporter exposes the metrics of Prusa printers to Prometheus. It reads
// the PrusaLink API at each scrape, and it receives the metrics that the printers send
// over UDP.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/pprof"
	"os"
	"os/signal"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/alecthomas/kong"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	versioncollector "github.com/prometheus/client_golang/prometheus/collectors/version"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/prometheus/common/promslog"
	"github.com/prometheus/common/version"
	"github.com/prometheus/exporter-toolkit/web"
	"golang.org/x/sync/errgroup"

	"github.com/anyasabo/prusa_exporter/v3/internal/config"
	"github.com/anyasabo/prusa_exporter/v3/internal/logging"
	"github.com/anyasabo/prusa_exporter/v3/internal/prusalink"
	"github.com/anyasabo/prusa_exporter/v3/internal/udp"
)

const name = "prusa_exporter"

type flags struct {
	configFile     string
	telemetryPath  string
	prusaLinkPath  string
	udpPath        string
	udpListen      string
	udpPrefix      string
	gcodeEnabled   bool
	gcodeHost      string
	gcodeAll       bool
	gcodeExtra     []string
	lokiPushURL    string
	enablePprof    bool
	webFlags       *web.FlagConfig
	promslogConfig promslog.Config
}

// cli has the command-line flags. The names are the flag names of prusa_exporter 2.x,
// which used kingpin, and the web and log flags of exporter-toolkit and promslog.
type cli struct {
	ConfigFile    string   `name:"config.file" default:"prusa.yml" help:"Path of the configuration file."`
	TelemetryPath string   `name:"web.telemetry-path" default:"/metrics" help:"Path of the metrics about the exporter itself."`
	PrusaLinkPath string   `name:"exporter.metrics-path" default:"/metrics/prusalink" help:"Path of the PrusaLink metrics."`
	UDPPath       string   `name:"exporter.udp-metrics-path" default:"/metrics/udp" help:"Path of the UDP metrics."`
	UDPListen     string   `name:"udp.listen-address" default:":8514" help:"UDP address for the metrics of the printers."`
	UDPPrefix     string   `name:"udp.prefix" default:"prusa_" help:"Prefix of the UDP metric names."`
	GcodeEnabled  bool     `name:"udp.gcode-enabled" default:"true" negatable:"" help:"At start, send a G-code to each printer that enables its UDP metrics to this exporter."`
	GcodeHost     string   `name:"udp.ip-override" placeholder:"ADDRESS" help:"Address of the exporter in the G-code. Default: the local address of the route to the printer."`
	GcodeAll      bool     `name:"udp.all-metrics" negatable:"" help:"Enable all UDP metrics in the G-code. This makes the printer slow."`
	GcodeExtra    []string `name:"udp.extra-metrics" placeholder:"METRIC" sep:"none" help:"A UDP metric to enable in the G-code, in addition to the default metrics. Repeat the flag for more metrics."`
	LokiPushURL   string   `name:"loki.push-url" placeholder:"URL" help:"Loki push URL for the job thumbnail, for example http://loki:3100/loki/api/v1/push. Empty disables the push."`
	EnablePprof   bool     `name:"web.enable-pprof" negatable:"" help:"Serve the Go profiles on /debug/pprof/. Protect them with --web.config.file if the address is not private."`

	// The flags of exporter-toolkit (web/kingpinflag) and promslog (promslog/flag).
	WebListen        []string `name:"web.listen-address" default:":10009" sep:"none" help:"Addresses on which to expose metrics and web interface. Repeatable for multiple addresses. Examples: :9100 or [::1]:9100 for http, vsock://:9100 for vsock."`
	WebSystemdSocket bool     `name:"web.systemd-socket" negatable:"" help:"Use systemd socket activation listeners instead of port listeners (Linux only)."`
	WebConfigFile    string   `name:"web.config.file" help:"Path to configuration file that can enable TLS or authentication. See: https://github.com/prometheus/exporter-toolkit/blob/master/docs/web-configuration.md"`
	LogLevel         string   `name:"log.level" default:"info" help:"Only log messages with the given severity or above. One of: [${log_levels}]"`
	LogFormat        string   `name:"log.format" default:"logfmt" help:"Output format of log messages. One of: [${log_formats}]"`

	Version kong.VersionFlag `help:"Show the version."`
}

// Validate implements the kong.Validatable interface.
func (c *cli) Validate() error {
	paths := map[string]bool{c.TelemetryPath: true, c.PrusaLinkPath: true, c.UDPPath: true}
	if len(paths) != 3 || paths["/"] {
		return errors.New("the telemetry, PrusaLink and UDP paths must be different, and not \"/\"")
	}
	if c.WebSystemdSocket && runtime.GOOS != "linux" {
		return errors.New("--web.systemd-socket works only on Linux")
	}
	return nil
}

// parseFlags parses the command-line arguments. For --help and --version, kong writes
// to stdout and exits. The tests give options to catch the output and the exit.
func parseFlags(args []string, options ...kong.Option) (*flags, error) {
	var c cli
	parser, err := kong.New(&c, append([]kong.Option{
		kong.Name(name),
		kong.Description("Prometheus exporter for Prusa printers."),
		kong.Vars{
			"version":     version.Print(name),
			"log_levels":  strings.Join(promslog.LevelFlagOptions, ","),
			"log_formats": strings.Join(promslog.FormatFlagOptions, ","),
		},
	}, options...)...)
	if err != nil {
		return nil, fmt.Errorf("flag definitions: %w", err)
	}
	if _, err := parser.Parse(args); err != nil {
		return nil, err //nolint:wrapcheck // kong errors are messages for the user.
	}
	f := &flags{
		configFile:    c.ConfigFile,
		telemetryPath: c.TelemetryPath,
		prusaLinkPath: c.PrusaLinkPath,
		udpPath:       c.UDPPath,
		udpListen:     c.UDPListen,
		udpPrefix:     c.UDPPrefix,
		gcodeEnabled:  c.GcodeEnabled,
		gcodeHost:     c.GcodeHost,
		gcodeAll:      c.GcodeAll,
		gcodeExtra:    c.GcodeExtra,
		lokiPushURL:   c.LokiPushURL,
		enablePprof:   c.EnablePprof,
		webFlags: &web.FlagConfig{
			WebListenAddresses: &c.WebListen,
			WebSystemdSocket:   &c.WebSystemdSocket,
			WebConfigFile:      &c.WebConfigFile,
		},
		promslogConfig: promslog.Config{Level: promslog.NewLevel(), Format: promslog.NewFormat()},
	}
	// Set checks the value. It accepts upper case, as kingpin's promslog/flag did.
	if err := f.promslogConfig.Level.Set(c.LogLevel); err != nil {
		return nil, fmt.Errorf("--log.level: %w", err)
	}
	if err := f.promslogConfig.Format.Set(c.LogFormat); err != nil {
		return nil, fmt.Errorf("--log.format: %w", err)
	}
	return f, nil
}

func main() {
	f, err := parseFlags(os.Args[1:])
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s: error: %v\n", name, err)
		os.Exit(1) // The exit code of kingpin's FatalIfError.
	}
	logger := logging.New(&f.promslogConfig, os.Stderr, name, version.Version)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	err = run(ctx, f, logger)
	stop()
	if err != nil {
		logger.Error("Exit with error", logging.ErrorMessage, err)
		os.Exit(1)
	}
}

func run(ctx context.Context, f *flags, logger *slog.Logger) error {
	logger.Info("Starting "+name, logging.BuildInfo, version.Info(), logging.BuildContext, version.BuildContext())

	cfg, err := config.Load(f.configFile)
	if err != nil {
		return err
	}
	for _, key := range cfg.Deprecated() {
		logger.Warn("The configuration file has a deprecated key. The exporter ignores it.", logging.ConfigKey, key)
	}
	for _, addr := range cfg.NotIPAddresses() {
		logger.Warn("The printer address is not a plain IP address. The PrusaLink and UDP series of this printer have different printer_address labels, so the alert rules that compare them do not work.",
			logging.PrinterAddress, addr)
	}

	// The registry of the exporter itself.
	self := prometheus.NewRegistry()
	self.MustRegister(
		versioncollector.NewCollector(name),
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
	)

	// PrusaLink.
	transport := prusalink.NewTransport()
	clientMetrics := prusalink.NewClientMetrics(self)
	targets := make([]*prusalink.Target, len(cfg.Printers))
	for i, p := range cfg.Printers {
		targets[i] = &prusalink.Target{Client: prusalink.NewClient(p, transport, clientMetrics)}
	}
	var loki *prusalink.LokiPusher
	if f.lokiPushURL != "" {
		loki = prusalink.NewLokiPusher(f.lokiPushURL, self)
	}
	opts := prusalink.Options{
		CommonLabels:   cfg.PrusaLink.CommonLabels,
		DisableMetrics: cfg.PrusaLink.DisableMetrics,
		Budget:         cfg.Exporter.PrusaLinkScrapeBudget,
		Loki:           loki,
	}
	plExporter := prusalink.NewExporter(targets, opts, logger, self)

	// UDP.
	udpAddr, err := net.ResolveUDPAddr("udp", f.udpListen)
	if err != nil {
		return fmt.Errorf("udp.listen-address: %w", err)
	}
	conn, err := net.ListenUDP("udp", udpAddr)
	if err != nil {
		return fmt.Errorf("listen on UDP %s: %w", f.udpListen, err)
	}
	udpCollector := udp.NewCollector(udp.Options{
		Prefix:           f.udpPrefix,
		SeriesTTL:        cfg.Exporter.UDPSeriesTTL,
		MaxSeries:        cfg.Exporter.UDPMaxSeries,
		SampleTimestamps: cfg.Exporter.UDPSampleTimestamps,
		EventMetrics:     cfg.Exporter.UDPEventMetrics,
		CounterFields:    cfg.Exporter.UDPCounterFields,
	}, logger, self)
	udpRegistry := prometheus.NewRegistry()
	udpRegistry.MustRegister(udpCollector)

	// HTTP.
	mux := http.NewServeMux()
	httpDuration := prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "prusa_exporter_http_request_duration_seconds",
		Help:    "Duration of the HTTP requests to the metrics endpoints of the exporter.",
		Buckets: []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10},
	}, []string{"handler", "code"})
	self.MustRegister(httpDuration)
	instrument := func(handler string, h http.Handler) http.Handler {
		return promhttp.InstrumentHandlerDuration(httpDuration.MustCurryWith(prometheus.Labels{"handler": handler}), h)
	}
	// Only the metrics endpoints: pprof requests can last 30 s, and nothing scrapes the landing page.
	mux.Handle(f.telemetryPath, instrument("metrics", promhttp.InstrumentMetricHandler(self, promhttp.HandlerFor(self, promhttp.HandlerOpts{}))))
	mux.Handle(f.prusaLinkPath, instrument("prusalink", plExporter.Handler()))
	mux.Handle(f.udpPath, instrument("udp", promhttp.HandlerFor(udpRegistry, promhttp.HandlerOpts{
		ErrorLog:      slog.NewLogLogger(logger.Handler(), slog.LevelError),
		ErrorHandling: promhttp.ContinueOnError,
		Registry:      self,
	})))
	landing, err := web.NewLandingPage(web.LandingConfig{
		Name:        name,
		Description: "Prometheus exporter for Prusa printers. UDP metrics on " + f.udpListen + ".",
		Version:     version.Info(),
		Profiling:   strconv.FormatBool(f.enablePprof),
		Links: []web.LandingLinks{
			{Address: f.prusaLinkPath, Text: "PrusaLink metrics"},
			{Address: f.udpPath, Text: "UDP metrics"},
			{Address: f.telemetryPath, Text: "Exporter metrics"},
		},
	})
	if err != nil {
		return fmt.Errorf("landing page: %w", err)
	}
	mux.Handle("/", landing)
	if f.enablePprof {
		mux.HandleFunc("/debug/pprof/", pprof.Index)
		mux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
		mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
		mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
		mux.HandleFunc("/debug/pprof/trace", pprof.Trace)
	}
	server := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       2 * time.Minute,
	}

	g, ctx := errgroup.WithContext(ctx)
	g.Go(func() error {
		logger.Info("Listening for UDP metrics", logging.ServerAddress, conn.LocalAddr().String())
		return udpCollector.Serve(ctx, conn)
	})
	g.Go(func() error {
		if err := web.ListenAndServe(server, f.webFlags, logger); !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("serve HTTP: %w", err)
		}
		return nil
	})
	g.Go(func() error {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		return server.Shutdown(shutdownCtx)
	})
	if f.gcodeEnabled {
		g.Go(func() error {
			prusalink.RunGcode(ctx, targets, prusalink.GcodeOptions{
				Host:          f.gcodeHost,
				Port:          udpAddr.Port,
				All:           f.gcodeAll,
				Extra:         f.gcodeExtra,
				Timeout:       30 * time.Second,
				RetryInterval: time.Minute,
			}, logger)
			return nil
		})
	} else {
		logger.Info("The G-code that enables the UDP metrics is disabled")
	}
	return g.Wait()
}
