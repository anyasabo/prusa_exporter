package config

import (
	"os"
	"strings"
	"time"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"gopkg.in/yaml.v3"
)

const (
	// DefaultUDPSeriesTTL is the default of ExporterConfig.UDPSeriesTTL.
	DefaultUDPSeriesTTL = 10 * time.Minute
	// DefaultPrusaLinkScrapeBudget is the default of ExporterConfig.PrusaLinkScrapeBudget.
	DefaultPrusaLinkScrapeBudget = 8 * time.Second
)

// Config struct for the configuration file prusa.yml
type Config struct {
	Exporter  ExporterConfig `yaml:"exporter"`
	Printers  []Printers     `yaml:"printers"`
	PrusaLink struct {
		CommonLabels   []string `yaml:"common_labels"`
		DisableMetrics []string `yaml:"disable_metrics"`
	} `yaml:"prusalink"`
}

// ExporterConfig is the "exporter" section of prusa.yml.
type ExporterConfig struct {
	ScrapeTimeout int    `yaml:"scrape_timeout"`
	LogLevel      string `yaml:"log_level"`
	IPOverride    string
	AllMetricsUDP bool
	ExtraMetrics  []string
	LokiPushURL   string
	// UDPSeriesTTL is the time after which a UDP gauge or info series without a new
	// point goes away. Counters do not expire. Zero or less means DefaultUDPSeriesTTL.
	UDPSeriesTTL time.Duration `yaml:"udp_series_ttl"`
	// UDPSampleTimestamps adds the estimated printer time of the last point to each
	// UDP sample. The scraper must honor the timestamps.
	UDPSampleTimestamps bool `yaml:"udp_sample_timestamps"`
	// PrusaLinkScrapeBudget is the maximum time for all PrusaLink requests of one
	// printer in one scrape. Keep it below the scrape timeout of the scraper.
	// Zero or less means DefaultPrusaLinkScrapeBudget.
	PrusaLinkScrapeBudget time.Duration `yaml:"prusalink_scrape_budget"`
}

// Printers struct containing the printer configuration
type Printers struct {
	Address           string `yaml:"address"`
	Username          string `yaml:"username,omitempty"`
	Password          string `yaml:"password,omitempty"`
	Apikey            string `yaml:"apikey,omitempty"`
	Name              string `yaml:"name,omitempty"`
	Type              string `yaml:"type,omitempty"`
	Reachable         bool
	UDPMetricsEnabled bool
}

// LoadConfig function to load and parse the configuration file
func LoadConfig(path string, prusaLinkScrapeTimeout int, udpIPOverride string, udpAllMetrics bool, udpExtraMetrics string, lokiPushURL string, lokiEnabled bool) (Config, error) {
	var config Config
	file, err := os.ReadFile(path)

	if err != nil {
		return config, err
	}

	if err := yaml.Unmarshal(file, &config); err != nil {
		return config, err
	}
	config.Exporter.ScrapeTimeout = prusaLinkScrapeTimeout
	if config.Exporter.UDPSeriesTTL <= 0 {
		config.Exporter.UDPSeriesTTL = DefaultUDPSeriesTTL
	}
	if config.Exporter.PrusaLinkScrapeBudget <= 0 {
		config.Exporter.PrusaLinkScrapeBudget = DefaultPrusaLinkScrapeBudget
	}
	if udpIPOverride != "" {
		config.Exporter.IPOverride = udpIPOverride
		log.Info().Msgf("Overriding IP address for UDP metrics: %s", udpIPOverride)
	}

	config.Exporter.AllMetricsUDP = udpAllMetrics
	if udpAllMetrics {
		log.Warn().Msg("Exposing all UDP metrics. This will severely impact CPU capabilities of the printer!")
	}

	if udpExtraMetrics != "" {
		splitMetrics := strings.Split(udpExtraMetrics, ",")
		config.Exporter.ExtraMetrics = splitMetrics
		log.Info().Msgf("Adding extra UDP metrics: %v", splitMetrics)
	}

	if lokiEnabled {
		config.Exporter.LokiPushURL = lokiPushURL
		log.Info().Msg("Loki integration enabled")
	} else {
		config.Exporter.LokiPushURL = ""
		log.Info().Msg("Loki integration disabled")
	}

	return config, err
}

// GetLogLevel function to parse the log level for zerolog
func GetLogLevel(level string) zerolog.Level {
	switch level {
	case "info":
		return zerolog.InfoLevel
	case "debug":
		return zerolog.DebugLevel
	case "trace":
		return zerolog.TraceLevel
	case "error":
		return zerolog.ErrorLevel
	case "panic":
		return zerolog.PanicLevel
	case "fatal":
		return zerolog.FatalLevel
	default:
		return zerolog.InfoLevel
	}
}
