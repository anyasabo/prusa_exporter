// Package logging makes the logger of the exporter and holds the names of the log
// keys. The keys follow the Elastic Common Schema (ECS): an ECS field where one
// exists, else a field in the custom namespace "prusa".
//
// The JSON format follows the ECS logging specification
// (https://github.com/elastic/ecs-logging/blob/main/spec/spec.json). The logfmt
// format is the format of promslog, for people.
package logging

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/prometheus/common/promslog"
)

// ECSVersion is the ECS version of the field names.
const ECSVersion = "9.5.0"

// The log keys. Use these constants, not strings: sloglint (no-raw-keys) makes sure.
const (
	// ECS fields.
	ErrorMessage  = "error.message"
	ServerAddress = "server.address"
	SourceIP      = "source.ip"
	SourcePort    = "source.port"

	// Custom fields.
	PrinterAddress = "prusa.printer.address"
	PrinterName    = "prusa.printer.name"
	PrinterMAC     = "prusa.printer.mac"
	MetricName     = "prusa.metric.name"
	MetricLabels   = "prusa.metric.labels"
	PointError     = "prusa.udp.point_error"
	PointLine      = "prusa.udp.line"
	SeriesLimit    = "prusa.udp.series_limit"
	ConfigKey      = "prusa.config.key"
	RetryIn        = "prusa.retry_in"
	BuildInfo      = "prusa.build.info"
	BuildContext   = "prusa.build.context"
	WebHTTP2       = "prusa.web.http2"
	RateBurst      = "prusa.web.rate_limit.burst"
	RateInterval   = "prusa.web.rate_limit.interval"
)

// libraryKeys maps the log keys of the libraries (exporter-toolkit) to the ECS keys.
var libraryKeys = map[string]string{
	"err":      ErrorMessage,
	"address":  ServerAddress,
	"http2":    WebHTTP2,
	"burst":    RateBurst,
	"interval": RateInterval,
}

// Keys of the ECS logging specification.
const (
	timestampKey  = "@timestamp"
	levelKey      = "log.level"
	messageKey    = "message"
	ecsVersionKey = "ecs.version"
	serviceName   = "service.name"
	serviceVer    = "service.version"
	originFile    = "log.origin.file.name"
	originLine    = "log.origin.file.line"
	originFunc    = "log.origin.function"
)

// New returns the logger for the format and level in cfg. For the JSON format it is
// an ECS logger with the service name and version.
func New(cfg *promslog.Config, w io.Writer, service, version string) *slog.Logger {
	if cfg.Format == nil || cfg.Format.String() != "json" {
		if w != nil {
			cfg.Writer = w
		}
		return promslog.New(cfg)
	}
	var level slog.Leveler = slog.LevelInfo
	if cfg.Level != nil {
		level = cfg.Level
	}
	h := slog.NewJSONHandler(w, &slog.HandlerOptions{Level: level, ReplaceAttr: replaceAttr})
	return slog.New(originHandler{h}).With(ecsVersionKey, ECSVersion, serviceName, service, serviceVer, version)
}

// replaceAttr gives the built-in keys and the library keys their ECS names.
func replaceAttr(groups []string, a slog.Attr) slog.Attr {
	if len(groups) > 0 {
		return a
	}
	if k, ok := libraryKeys[a.Key]; ok {
		return slog.Attr{Key: k, Value: a.Value} //nolint:sloglint // k is a constant from libraryKeys.
	}
	switch a.Key {
	case slog.TimeKey:
		return slog.Time(timestampKey, a.Value.Time().UTC())
	case slog.LevelKey:
		level, _ := a.Value.Any().(slog.Level)
		return slog.String(levelKey, strings.ToLower(level.String()))
	case slog.MessageKey:
		return slog.Attr{Key: messageKey, Value: a.Value}
	}
	return a
}

// originHandler adds the log.origin fields after the message. The spec wants the
// message as the third field, but the slog source comes before the message.
type originHandler struct{ slog.Handler }

func (h originHandler) Handle(ctx context.Context, r slog.Record) error {
	if r.PC != 0 {
		f, _ := runtime.CallersFrames([]uintptr{r.PC}).Next()
		r.AddAttrs(
			slog.String(originFile, filepath.Base(f.File)),
			slog.Int(originLine, f.Line),
			slog.String(originFunc, f.Function),
		)
	}
	return h.Handler.Handle(ctx, r) //nolint:wrapcheck // A handler passes the error through.
}

func (h originHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return originHandler{h.Handler.WithAttrs(attrs)}
}

func (h originHandler) WithGroup(name string) slog.Handler {
	return originHandler{h.Handler.WithGroup(name)}
}
