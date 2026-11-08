package prusalink

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

const (
	gcodeFile = "enable_udp_metrics.gcode"
	// gcodeSyslogPort is the port for the syslog of the printer (M340). The exporter
	// does not listen on it. A log collector can.
	gcodeSyslogPort = 13514
)

// GcodeOptions are the settings of the G-code that enables the UDP metrics.
type GcodeOptions struct {
	// Host is the address of the exporter for the printer. If empty, the exporter uses
	// its local address of the route to the printer.
	Host string
	// Port is the UDP port of the exporter.
	Port int
	// All enables all metrics. This can make the printer slow.
	All bool
	// Extra are metrics to enable in addition to DefaultUDPMetrics.
	Extra []string
	// Timeout limits each attempt.
	Timeout time.Duration
	// RetryInterval is the time between two attempts for a printer that failed.
	RetryInterval time.Duration
}

// Gcode returns the G-code that enables the UDP metrics, with host as the address
// of the exporter.
func (o GcodeOptions) Gcode(host string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "M330 SYSLOG\nM334 %s %d\nM340 %s %d", host, o.Port, host, gcodeSyslogPort)
	if o.All {
		for _, m := range AllUDPMetrics {
			b.WriteString("\nM331 " + m)
		}
		return b.String()
	}
	// Disable all metrics first. This is less work for the printer.
	for _, m := range AllUDPMetrics {
		b.WriteString("\nM332 " + m)
	}
	for _, m := range DefaultUDPMetrics {
		b.WriteString("\nM331 " + m)
	}
	for _, m := range o.Extra {
		b.WriteString("\nM331 " + m)
	}
	return b.String()
}

// localAddress returns the local IP address of the route to the printer. A UDP
// "connection" sends no packet. The kernel only selects the route.
func localAddress(ctx context.Context, printerAddress string) (string, error) {
	host := printerAddress
	if h, _, err := net.SplitHostPort(printerAddress); err == nil {
		host = h
	}
	conn, err := (&net.Dialer{}).DialContext(ctx, "udp", net.JoinHostPort(host, "80"))
	if err != nil {
		return "", err
	}
	defer conn.Close()
	addr, ok := conn.LocalAddr().(*net.UDPAddr)
	if !ok {
		return "", errors.New("no UDP address")
	}
	return addr.IP.String(), nil
}

// EnableUDPMetrics uploads and starts the G-code on the printer.
func EnableUDPMetrics(ctx context.Context, c *Client, o GcodeOptions) error {
	host := o.Host
	if host == "" {
		var err error
		if host, err = localAddress(ctx, c.Printer.Address); err != nil {
			return fmt.Errorf("find the local address: %w", err)
		}
	}
	path := "/api/v1/files/usb//" + gcodeFile
	const endpoint = "/api/v1/files"
	// The Overwrite header replaces an old file, but old firmware can ignore it.
	_, _ = c.Do(ctx, http.MethodDelete, endpoint, path, nil, nil)
	header := http.Header{"Content-Type": {"text/x.gcode"}, "Overwrite": {"?1"}}
	if _, err := c.Do(ctx, http.MethodPut, endpoint, path, strings.NewReader(o.Gcode(host)), header); err != nil && !errors.Is(err, ErrNoContent) {
		return fmt.Errorf("upload: %w", err)
	}
	if _, err := c.Do(ctx, http.MethodPost, endpoint, path, nil, nil); err != nil && !errors.Is(err, ErrNoContent) {
		return fmt.Errorf("start: %w", err)
	}
	return nil
}

// RunGcode enables the UDP metrics on each target. It tries again after a failure
// until ctx ends. It returns when all targets succeeded or ctx ended.
func RunGcode(ctx context.Context, targets []*Target, o GcodeOptions, logger *slog.Logger) {
	var wg sync.WaitGroup
	for _, t := range targets {
		wg.Go(func() {
			l := logger.With("printer_address", t.Client.Printer.Address, "printer_name", t.Client.Printer.Name)
			for {
				attempt, cancel := context.WithTimeout(ctx, o.Timeout)
				err := EnableUDPMetrics(attempt, t.Client, o)
				cancel()
				if err == nil {
					t.GcodeSent.Store(true)
					l.Info("Sent and started the G-code that enables the UDP metrics")
					return
				}
				l.Warn("Cannot enable the UDP metrics, will try again", "err", err, "retry_in", o.RetryInterval)
				select {
				case <-ctx.Done():
					return
				case <-time.After(o.RetryInterval):
				}
			}
		})
	}
	wg.Wait()
}
