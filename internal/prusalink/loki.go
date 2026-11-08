package prusalink

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/anyasabo/prusa_exporter/internal/config"
	"github.com/anyasabo/prusa_exporter/internal/logging"
)

// lokiRepushInterval is the time after which the exporter sends the thumbnail of the
// same job again. The dashboard finds the thumbnail in its time range.
const lokiRepushInterval = 5 * time.Minute

// lokiState is the push state of one printer.
type lokiState struct {
	mu       sync.Mutex
	path     string
	pushed   time.Time
	inFlight bool
}

// LokiPusher sends the thumbnail of the current job to Grafana Loki.
type LokiPusher struct {
	url     string
	http    *http.Client
	timeout time.Duration
	pushes  *prometheus.CounterVec
	now     func() time.Time
}

// NewLokiPusher returns a pusher for the Loki push URL, for example
// "http://loki:3100/loki/api/v1/push".
func NewLokiPusher(url string, reg prometheus.Registerer) *LokiPusher {
	p := &LokiPusher{
		url:     url,
		http:    &http.Client{},
		timeout: 30 * time.Second,
		pushes: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "prusa_exporter_loki_pushes_total",
			Help: "Number of job thumbnail pushes to Loki, by result.",
		}, []string{"result"}),
		now: time.Now,
	}
	for _, r := range []string{"success", "thumbnail_error", "push_error"} {
		p.pushes.WithLabelValues(r)
	}
	reg.MustRegister(p.pushes)
	return p
}

// MaybePush starts a push in the background if the job is new, or if the last push of
// the job is older than lokiRepushInterval. It does nothing while a push of the
// printer runs. The push continues after ctx ends, but it has its own timeout.
func (p *LokiPusher) MaybePush(ctx context.Context, t *Target, jobName, jobPath string, logger *slog.Logger) {
	st := &t.loki
	st.mu.Lock()
	defer st.mu.Unlock()
	now := p.now()
	if st.inFlight || (st.path == jobPath && now.Sub(st.pushed) < lokiRepushInterval) {
		return
	}
	st.inFlight = true
	go func() {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), p.timeout)
		defer cancel()
		ok := p.push(ctx, t.Client, jobName, jobPath, logger)
		st.mu.Lock()
		defer st.mu.Unlock()
		st.inFlight = false
		if ok {
			st.path, st.pushed = jobPath, now
		}
	}()
}

func (p *LokiPusher) push(ctx context.Context, c *Client, jobName, jobPath string, logger *slog.Logger) bool {
	thumb, err := c.Thumbnail(ctx, jobPath)
	if err == nil {
		thumb, err = recompressPNG(thumb)
	}
	if err != nil {
		p.pushes.WithLabelValues("thumbnail_error").Inc()
		logger.Warn("Cannot get the job thumbnail", logging.ErrorMessage, err)
		return false
	}
	if err := p.send(ctx, c, jobName, jobPath, base64.StdEncoding.EncodeToString(thumb)); err != nil {
		p.pushes.WithLabelValues("push_error").Inc()
		logger.Warn("Cannot push the job thumbnail to Loki", logging.ErrorMessage, err)
		return false
	}
	p.pushes.WithLabelValues("success").Inc()
	return true
}

type lokiPush struct {
	Streams []lokiStream `json:"streams"`
}

type lokiStream struct {
	Stream map[string]string `json:"stream"`
	Values [][2]string       `json:"values"`
}

func (p *LokiPusher) send(ctx context.Context, c *Client, jobName, jobPath, image string) error {
	pr := c.Printer
	payload, err := json.Marshal(lokiPush{Streams: []lokiStream{{
		Stream: map[string]string{
			"job":               "prusa_job_image",
			config.LabelAddress: pr.Address,
			config.LabelModel:   pr.Type,
			config.LabelName:    pr.Name,
			config.LabelJobName: jobName,
			config.LabelJobPath: jobPath,
		},
		Values: [][2]string{{strconv.FormatInt(p.now().UnixNano(), 10), image}},
	}}})
	if err != nil {
		return fmt.Errorf("encode the Loki push: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.url, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("loki: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := p.http.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(res.Body, 64<<10)) // Read the body, so the connection can be used again.
	if res.StatusCode < 200 || res.StatusCode > 299 {
		return fmt.Errorf("loki: %s", res.Status)
	}
	return nil
}

// recompressPNG decodes the thumbnail and encodes it as a PNG with the best compression.
// The decode also makes sure that the data is an image.
func recompressPNG(data []byte) ([]byte, error) {
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("decode thumbnail: %w", err)
	}
	var b bytes.Buffer
	if err := (&png.Encoder{CompressionLevel: png.BestCompression}).Encode(&b, img); err != nil {
		return nil, fmt.Errorf("encode thumbnail: %w", err)
	}
	return b.Bytes(), nil
}
