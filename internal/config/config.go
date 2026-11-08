// Package config reads and checks prusa.yml.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"time"

	"go.yaml.in/yaml/v3"
)

const (
	// DefaultUDPSeriesTTL is the default of Exporter.UDPSeriesTTL.
	DefaultUDPSeriesTTL = 10 * time.Minute
	// DefaultUDPMaxSeries is the default of Exporter.UDPMaxSeries.
	DefaultUDPMaxSeries = 10000
	// DefaultPrusaLinkScrapeBudget is the default of Exporter.PrusaLinkScrapeBudget.
	DefaultPrusaLinkScrapeBudget = 8 * time.Second
)

// DefaultUDPEventMetrics is the default of Exporter.UDPEventMetrics. These are all
// METRIC_VALUE_EVENT metrics of the Prusa Buddy firmware.
var DefaultUDPEventMetrics = []string{"app_start", "crash_repeated", "maintask_loop", "modbus_reqfail", "power_panic", "probe_start"}

// The label names of the PrusaLink metrics and the Loki streams.
const (
	LabelAddress = "printer_address"
	LabelModel   = "printer_model"
	LabelName    = "printer_name"
	LabelJobName = "printer_job_name"
	LabelJobPath = "printer_job_path"
)

// LabelNames are the values that PrusaLink.CommonLabels can have.
var LabelNames = []string{LabelAddress, LabelModel, LabelName, LabelJobName, LabelJobPath}

// DefaultCommonLabels are the common labels if prusa.yml sets none. The job labels
// are not in the list, because they make new series for each print.
var DefaultCommonLabels = []string{LabelAddress, LabelModel, LabelName}

// Config is the content of prusa.yml.
type Config struct {
	Exporter  Exporter  `yaml:"exporter"`
	Printers  []Printer `yaml:"printers"`
	PrusaLink PrusaLink `yaml:"prusalink"`
}

// Exporter is the "exporter" section of prusa.yml.
type Exporter struct {
	// UDPSeriesTTL is the time after which a UDP gauge or info series without a new
	// point goes away. Counters do not expire.
	UDPSeriesTTL time.Duration `yaml:"udp_series_ttl"`
	// UDPSampleTimestamps adds the estimated printer time of the last point to each
	// UDP sample. The scraper must honor the timestamps.
	UDPSampleTimestamps bool `yaml:"udp_sample_timestamps"`
	// UDPMaxSeries is the maximum number of UDP series. The exporter drops the points
	// of new series above this number.
	UDPMaxSeries int `yaml:"udp_max_series"`
	// UDPEventMetrics are the firmware names of the UDP event metrics (v=T). The first
	// packet of a printer makes prusa_<name>_total at 0 for each name. If the key is
	// not set, the list is DefaultUDPEventMetrics. An empty list disables it.
	UDPEventMetrics []string `yaml:"udp_event_metrics"`
	// PrusaLinkScrapeBudget is the maximum time for all PrusaLink requests of one
	// printer in one scrape. The scrape timeout of the scraper can make it shorter.
	PrusaLinkScrapeBudget time.Duration `yaml:"prusalink_scrape_budget"`

	// Deprecated keys. Load ignores them, so that old files stay valid.
	DeprecatedScrapeTimeout any `yaml:"scrape_timeout"`
	DeprecatedLogLevel      any `yaml:"log_level"`
}

// PrusaLink is the "prusalink" section of prusa.yml.
type PrusaLink struct {
	// CommonLabels are the labels on each PrusaLink metric, from LabelNames.
	CommonLabels []string `yaml:"common_labels"`
	// DisableMetrics are the names of the PrusaLink metrics to not expose.
	DisableMetrics []string `yaml:"disable_metrics"`
}

// Printer is one printer in prusa.yml.
type Printer struct {
	// Address is the host and an optional port of PrusaLink, for example "192.168.1.10".
	Address  string `yaml:"address"`
	Username string `yaml:"username"`
	Password string `yaml:"password"`
	APIKey   string `yaml:"apikey"`
	Name     string `yaml:"name"`
	// Type is the printer model, for example "MK4". It is the label printer_model.
	Type string `yaml:"type"`
}

// Load reads the file at path, sets the defaults and checks the values.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path) //nolint:gosec // The operator gives the path.
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	return Parse(data)
}

// Parse decodes a prusa.yml file, sets the defaults and checks the values.
// An unknown key is an error.
func Parse(data []byte) (*Config, error) {
	var c Config
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&c); err != nil && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("decode config: %w", err)
	}
	c.setDefaults()
	if err := c.validate(); err != nil {
		return nil, err
	}
	return &c, nil
}

func (c *Config) setDefaults() {
	if c.Exporter.UDPSeriesTTL <= 0 {
		c.Exporter.UDPSeriesTTL = DefaultUDPSeriesTTL
	}
	if c.Exporter.UDPMaxSeries <= 0 {
		c.Exporter.UDPMaxSeries = DefaultUDPMaxSeries
	}
	if c.Exporter.PrusaLinkScrapeBudget <= 0 {
		c.Exporter.PrusaLinkScrapeBudget = DefaultPrusaLinkScrapeBudget
	}
	if c.Exporter.UDPEventMetrics == nil {
		c.Exporter.UDPEventMetrics = slices.Clone(DefaultUDPEventMetrics)
	}
	if len(c.PrusaLink.CommonLabels) == 0 {
		c.PrusaLink.CommonLabels = slices.Clone(DefaultCommonLabels)
	}
}

func (c *Config) validate() error {
	var errs []error
	for _, l := range c.PrusaLink.CommonLabels {
		if !slices.Contains(LabelNames, l) {
			errs = append(errs, fmt.Errorf("prusalink.common_labels: unknown label %q, use one of %v", l, LabelNames))
		}
	}
	seen := make(map[string]bool, len(c.Printers))
	for i, p := range c.Printers {
		switch {
		case p.Address == "":
			errs = append(errs, fmt.Errorf("printers[%d]: address is empty", i))
		case seen[p.Address]:
			errs = append(errs, fmt.Errorf("printers[%d]: address %q is a duplicate", i, p.Address))
		}
		seen[p.Address] = true
		if p.APIKey == "" && p.Username == "" {
			errs = append(errs, fmt.Errorf("printers[%d] (%s): set apikey, or username and password", i, p.Address))
		}
	}
	return errors.Join(errs...)
}

// Deprecated returns the deprecated keys that the file sets.
func (c *Config) Deprecated() []string {
	var keys []string
	if c.Exporter.DeprecatedScrapeTimeout != nil {
		keys = append(keys, "exporter.scrape_timeout")
	}
	if c.Exporter.DeprecatedLogLevel != nil {
		keys = append(keys, "exporter.log_level")
	}
	return keys
}
