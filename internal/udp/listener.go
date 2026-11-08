package udp

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/netip"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// The values of the label "result" of prusa_exporter_udp_points_total.
const (
	resultStored     = "stored"
	resultParseError = "parse_error"
	resultErrorPoint = "error_point"
	resultDropped    = "dropped"
)

// readBufferSize is the socket receive buffer. A printer sends bursts of datagrams.
const readBufferSize = 1 << 20

type selfMetrics struct {
	packets         prometheus.Counter
	bytes           prometheus.Counter
	readErrors      prometheus.Counter
	invalidMessages prometheus.Counter
	pointsStored    prometheus.Counter
	parseErrors     prometheus.Counter
	errorPoints     prometheus.Counter
	pointsDropped   prometheus.Counter
	seriesExpired   prometheus.Counter
}

func newSelfMetrics(reg prometheus.Registerer, seriesCount func() float64) *selfMetrics {
	points := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "prusa_exporter_udp_points_total",
		Help: "Number of UDP points, by result. \"dropped\" is a point of a new series above the series limit, or with a conflicting type.",
	}, []string{"result"})
	m := &selfMetrics{
		packets: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "prusa_exporter_udp_packets_received_total",
			Help: "Number of UDP datagrams received.",
		}),
		bytes: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "prusa_exporter_udp_received_bytes_total",
			Help: "Number of bytes in the UDP datagrams received.",
		}),
		readErrors: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "prusa_exporter_udp_read_errors_total",
			Help: "Number of errors while reading from the UDP socket.",
		}),
		invalidMessages: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "prusa_exporter_udp_invalid_messages_total",
			Help: "Number of UDP datagrams without a valid syslog header.",
		}),
		// The counters of each result, so that a point does not look up the label.
		pointsStored:  points.WithLabelValues(resultStored),
		parseErrors:   points.WithLabelValues(resultParseError),
		errorPoints:   points.WithLabelValues(resultErrorPoint),
		pointsDropped: points.WithLabelValues(resultDropped),
		seriesExpired: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "prusa_exporter_udp_series_expired_total",
			Help: "Number of UDP series deleted because they had no point in the TTL.",
		}),
	}
	reg.MustRegister(m.packets, m.bytes, m.readErrors, m.invalidMessages, points, m.seriesExpired,
		prometheus.NewGaugeFunc(prometheus.GaugeOpts{
			Name: "prusa_exporter_udp_series",
			Help: "Number of UDP series in memory.",
		}, seriesCount))
	return m
}

// Handle processes one datagram from the address from.
func (c *Collector) Handle(ctx context.Context, data []byte, from netip.AddrPort, received time.Time) {
	c.metrics.packets.Inc()
	c.metrics.bytes.Add(float64(len(data)))

	mac, lines, err := parseMessage(string(data))
	if err != nil {
		c.metrics.invalidMessages.Inc()
		if c.logger.Enabled(ctx, slog.LevelDebug) {
			c.logger.DebugContext(ctx, "Invalid UDP message", "from", from, "err", err)
		}
		return
	}
	s := scratchPool.Get().(*scratch)
	defer scratchPool.Put(s)
	s.reset()
	for line, rest := nextLine(lines); line != ""; line, rest = nextLine(rest) {
		p, err := s.parsePoint(line)
		if err != nil {
			// The firmware can send a raw value that is not a point, for example from tmc_read.
			c.metrics.parseErrors.Inc()
			if c.logger.Enabled(ctx, slog.LevelDebug) {
				c.logger.DebugContext(ctx, "Cannot parse a UDP point", "line", line, "err", err)
			}
			continue
		}
		s.points = append(s.points, p)
	}
	setSampleTimes(s.points, received)
	stored, dropped := c.record(ctx, mac, from.Addr().Unmap(), s.points, received)
	c.metrics.pointsStored.Add(float64(stored))
	c.metrics.pointsDropped.Add(float64(dropped))
}

// Serve reads datagrams from conn until ctx ends, and gives them to c. It closes conn.
func (c *Collector) Serve(ctx context.Context, conn *net.UDPConn) error {
	if err := conn.SetReadBuffer(readBufferSize); err != nil {
		c.logger.Warn("Cannot set the UDP receive buffer", "err", err)
	}
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()

	buf := make([]byte, 64<<10)
	for {
		n, from, err := conn.ReadFromUDPAddrPort(buf)
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				return nil
			}
			c.metrics.readErrors.Inc()
			c.logger.Warn("Cannot read from the UDP socket", "err", err)
			continue
		}
		c.Handle(ctx, buf[:n], from, time.Now())
	}
}
