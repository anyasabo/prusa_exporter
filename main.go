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
	"strconv"
	"syscall"
	"time"

	"github.com/alecthomas/kingpin/v2"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	versioncollector "github.com/prometheus/client_golang/prometheus/collectors/version"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/prometheus/common/promslog"
	promslogflag "github.com/prometheus/common/promslog/flag"
	"github.com/prometheus/common/version"
	"github.com/prometheus/exporter-toolkit/web"
	"github.com/prometheus/exporter-toolkit/web/kingpinflag"
	"golang.org/x/sync/errgroup"

	"github.com/anyasabo/prusa_exporter/internal/config"
	"github.com/anyasabo/prusa_exporter/internal/logging"
	"github.com/anyasabo/prusa_exporter/internal/prusalink"
	"github.com/anyasabo/prusa_exporter/internal/udp"
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

func parseFlags(app *kingpin.Application, args []string) (*flags, error) {
	f := &flags{}
	app.Flag("config.file", "Path of the configuration file.").Default("prusa.yml").StringVar(&f.configFile)
	app.Flag("web.telemetry-path", "Path of the metrics about the exporter itself.").Default("/metrics").StringVar(&f.telemetryPath)
	app.Flag("exporter.metrics-path", "Path of the PrusaLink metrics.").Default("/metrics/prusalink").StringVar(&f.prusaLinkPath)
	app.Flag("exporter.udp-metrics-path", "Path of the UDP metrics.").Default("/metrics/udp").StringVar(&f.udpPath)
	app.Flag("udp.listen-address", "UDP address for the metrics of the printers.").Default(":8514").StringVar(&f.udpListen)
	app.Flag("udp.prefix", "Prefix of the UDP metric names.").Default("prusa_").StringVar(&f.udpPrefix)
	app.Flag("udp.gcode-enabled", "At start, send a G-code to each printer that enables its UDP metrics to this exporter.").Default("true").BoolVar(&f.gcodeEnabled)
	app.Flag("udp.ip-override", "Address of the exporter in the G-code. Default: the local address of the route to the printer.").PlaceHolder("ADDRESS").StringVar(&f.gcodeHost)
	app.Flag("udp.all-metrics", "Enable all UDP metrics in the G-code. This makes the printer slow.").BoolVar(&f.gcodeAll)
	app.Flag("udp.extra-metrics", "A UDP metric to enable in the G-code, in addition to the default metrics. Repeat the flag for more metrics.").PlaceHolder("METRIC").StringsVar(&f.gcodeExtra)
	app.Flag("loki.push-url", "Loki push URL for the job thumbnail, for example http://loki:3100/loki/api/v1/push. Empty disables the push.").PlaceHolder("URL").StringVar(&f.lokiPushURL)
	app.Flag("web.enable-pprof", "Serve the Go profiles on /debug/pprof/. Protect them with --web.config.file if the address is not private.").BoolVar(&f.enablePprof)
	f.webFlags = kingpinflag.AddFlags(app, ":10009")
	promslogflag.AddFlags(app, &f.promslogConfig)
	app.Version(version.Print(name))
	app.HelpFlag.Short('h')
	app.Validate(func(*kingpin.Application) error {
		paths := map[string]bool{f.telemetryPath: true, f.prusaLinkPath: true, f.udpPath: true}
		if len(paths) != 3 || paths["/"] {
			return errors.New("the telemetry, PrusaLink and UDP paths must be different, and not \"/\"")
		}
		return nil
	})
	if _, err := app.Parse(args); err != nil {
		return nil, err //nolint:wrapcheck // kingpin errors are messages for the user, in the kingpin format.
	}
	return f, nil
}

func main() {
	app := kingpin.New(name, "Prometheus exporter for Prusa printers.")
	f, err := parseFlags(app, os.Args[1:])
	app.FatalIfError(err, "")
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
	opts := prusalink.Options{
		CommonLabels:   cfg.PrusaLink.CommonLabels,
		DisableMetrics: cfg.PrusaLink.DisableMetrics,
		Budget:         cfg.Exporter.PrusaLinkScrapeBudget,
	}
	if f.lokiPushURL != "" {
		opts.Loki = prusalink.NewLokiPusher(f.lokiPushURL, self)
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
		Help:    "Duration of the HTTP requests to the PrusaLink and UDP metrics endpoints.",
		Buckets: []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10},
	}, []string{"handler", "code"})
	self.MustRegister(httpDuration)
	instrument := func(handler string, h http.Handler) http.Handler {
		return promhttp.InstrumentHandlerDuration(httpDuration.MustCurryWith(prometheus.Labels{"handler": handler}), h)
	}
	mux.Handle(f.telemetryPath, promhttp.InstrumentMetricHandler(self, promhttp.HandlerFor(self, promhttp.HandlerOpts{})))
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
