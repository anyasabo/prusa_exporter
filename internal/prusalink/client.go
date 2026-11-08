// Package prusalink reads the PrusaLink API of Prusa printers and exposes the
// values as Prometheus metrics.
package prusalink

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"strconv"
	"time"

	"github.com/icholy/digest"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/anyasabo/prusa_exporter/internal/config"
)

// maxBodySize is the maximum size of a PrusaLink response that the client reads.
const maxBodySize = 8 << 20

// ErrNoContent is the error of a request that got "204 No Content".
var ErrNoContent = errors.New("no content")

// ClientMetrics are the metrics of the HTTP requests to PrusaLink. Each label set has
// the printer address, the endpoint and the status code ("error" if no response).
type ClientMetrics struct {
	duration *prometheus.HistogramVec
}

// NewClientMetrics makes and registers the client metrics.
func NewClientMetrics(reg prometheus.Registerer) *ClientMetrics {
	m := &ClientMetrics{
		duration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "prusa_exporter_prusalink_request_duration_seconds",
			Help:    "Duration of the HTTP requests to PrusaLink, including the digest challenges.",
			Buckets: []float64{0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10},
		}, []string{"printer_address", "endpoint", "code"}),
	}
	reg.MustRegister(m.duration)
	return m
}

type endpointKey struct{}

// instrumentedTransport records each request on the wire. A digest request can be
// two requests: the challenge (401) and the authenticated request.
type instrumentedTransport struct {
	next    http.RoundTripper
	address string
	metrics *ClientMetrics
}

func (t *instrumentedTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	endpoint, _ := req.Context().Value(endpointKey{}).(string)
	start := time.Now()
	res, err := t.next.RoundTrip(req)
	code := "error"
	if err == nil {
		code = strconv.Itoa(res.StatusCode)
	}
	t.metrics.duration.WithLabelValues(t.address, endpoint, code).Observe(time.Since(start).Seconds())
	return res, err
}

// apiKeyTransport adds the X-Api-Key header to each request.
type apiKeyTransport struct {
	next http.RoundTripper
	key  string
}

func (t *apiKeyTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	req.Header.Set("X-Api-Key", t.key)
	return t.next.RoundTrip(req)
}

// NewTransport returns the base transport for all printers. The printers have a small
// HTTP server, so the transport keeps few idle connections for each printer.
func NewTransport() *http.Transport {
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.MaxIdleConnsPerHost = 2
	t.IdleConnTimeout = 90 * time.Second
	t.ResponseHeaderTimeout = 30 * time.Second
	return t
}

// Client sends requests to the PrusaLink API of one printer. Use one Client for each
// printer for the life of the program: the digest transport keeps the challenge, so
// each request after the first needs only one round trip.
type Client struct {
	Printer config.Printer
	http    *http.Client
}

// NewClient returns a client for p. base is the shared transport. metrics can be nil.
func NewClient(p config.Printer, base http.RoundTripper, metrics *ClientMetrics) *Client {
	rt := base
	if metrics != nil {
		rt = &instrumentedTransport{next: rt, address: p.Address, metrics: metrics}
	}
	if p.APIKey != "" {
		rt = &apiKeyTransport{next: rt, key: p.APIKey}
	} else {
		rt = &digest.Transport{Username: p.Username, Password: p.Password, Transport: rt}
	}
	return &Client{
		Printer: p,
		http: &http.Client{
			Transport: rt,
			// PrusaLink does not redirect. A redirect is an error, not a new host.
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
	}
}

func (c *Client) url(path string) string {
	return "http://" + c.Printer.Address + path
}

// Do sends a request and returns the body of a 2xx response. endpoint is the value of
// the label "endpoint" in the client metrics. ctx limits the request and the body read.
func (c *Client) Do(ctx context.Context, method, endpoint, path string, body io.Reader, header http.Header) ([]byte, error) {
	ctx = context.WithValue(ctx, endpointKey{}, endpoint)
	req, err := http.NewRequestWithContext(ctx, method, c.url(path), body)
	if err != nil {
		return nil, err
	}
	maps.Copy(req.Header, header)
	res, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()

	data, err := io.ReadAll(io.LimitReader(res.Body, maxBodySize))
	if err != nil {
		return nil, fmt.Errorf("%s %s: read body: %w", method, path, err)
	}
	if res.StatusCode < 200 || res.StatusCode > 299 {
		return nil, fmt.Errorf("%s %s: %s", method, path, res.Status)
	}
	if res.StatusCode == http.StatusNoContent {
		return nil, ErrNoContent
	}
	return data, nil
}

func getJSON[T any](ctx context.Context, c *Client, path string) (T, error) {
	var v T
	data, err := c.Do(ctx, http.MethodGet, path, path, nil, nil)
	if err != nil {
		return v, err
	}
	if err := json.Unmarshal(data, &v); err != nil {
		return v, fmt.Errorf("GET %s: decode: %w", path, err)
	}
	return v, nil
}

// Status returns /api/v1/status.
func (c *Client) Status(ctx context.Context) (Status, error) {
	return getJSON[Status](ctx, c, "/api/v1/status")
}

// Info returns /api/v1/info.
func (c *Client) Info(ctx context.Context) (Info, error) {
	return getJSON[Info](ctx, c, "/api/v1/info")
}

// Version returns /api/version.
func (c *Client) Version(ctx context.Context) (Version, error) {
	return getJSON[Version](ctx, c, "/api/version")
}

// PrinterState returns /api/printer.
func (c *Client) PrinterState(ctx context.Context) (Printer, error) {
	return getJSON[Printer](ctx, c, "/api/printer")
}

// Job returns /api/job.
func (c *Client) Job(ctx context.Context) (Job, error) {
	return getJSON[Job](ctx, c, "/api/job")
}

// Thumbnail returns the large thumbnail of the file at path, for example
// "/usb/PYTHON~1.BGC".
func (c *Client) Thumbnail(ctx context.Context, path string) ([]byte, error) {
	return c.Do(ctx, http.MethodGet, "/thumb/l", "/thumb/l"+path, nil, nil)
}
