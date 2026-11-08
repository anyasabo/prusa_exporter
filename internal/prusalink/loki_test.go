package prusalink

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

type fakeLoki struct {
	mu     sync.Mutex
	pushes []lokiPush
	code   int
}

func (l *fakeLoki) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	l.mu.Lock()
	defer l.mu.Unlock()
	var p lokiPush
	body, _ := io.ReadAll(r.Body)
	if err := json.Unmarshal(body, &p); err == nil {
		l.pushes = append(l.pushes, p)
	}
	if l.code != 0 {
		w.WriteHeader(l.code)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (l *fakeLoki) count() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.pushes)
}

func testPNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 4, 4))
	img.Set(1, 1, color.RGBA{R: 255, A: 255})
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// waitFor waits until the result counter reaches n.
func waitFor(t *testing.T, p *LokiPusher, result string, n float64) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for testutil.ToFloat64(p.pushes.WithLabelValues(result)) < n {
		if time.Now().After(deadline) {
			t.Fatalf("%s pushes: %v, want %v", result, testutil.ToFloat64(p.pushes.WithLabelValues(result)), n)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// waitIdle waits until no push of the target runs.
func waitIdle(t *testing.T, target *Target) {
	t.Helper()
	for range 1000 {
		target.loki.mu.Lock()
		busy := target.loki.inFlight
		target.loki.mu.Unlock()
		if !busy {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("push did not end")
}

func TestLokiPush(t *testing.T) {
	t.Parallel()
	f := newFakePrusaLink(t, false)
	f.setRaw("/thumb/l/usb/BENCHY~1.BGC", string(testPNG(t)))
	loki := &fakeLoki{}
	ls := httptest.NewServer(loki)
	t.Cleanup(ls.Close)

	p := NewLokiPusher(ls.URL, prometheus.NewRegistry())
	now := time.Unix(1_700_000_000, 0)
	p.now = func() time.Time { return now }
	target := &Target{Client: NewClient(f.printer(), NewTransport(), nil)}

	p.MaybePush(t.Context(), target, "benchy.bgcode", "/usb/BENCHY~1.BGC", discardLogger())
	waitFor(t, p, "success", 1)
	waitIdle(t, target)

	loki.mu.Lock()
	got := loki.pushes[0].Streams[0]
	loki.mu.Unlock()
	if got.Stream["job"] != "prusa_job_image" || got.Stream["printer_job_path"] != "/usb/BENCHY~1.BGC" || got.Stream["printer_name"] != "Test" {
		t.Errorf("stream labels = %v", got.Stream)
	}
	if got.Values[0][0] != "1700000000000000000" {
		t.Errorf("timestamp = %s, want nanoseconds", got.Values[0][0])
	}
	img, err := base64.StdEncoding.DecodeString(got.Values[0][1])
	if err != nil {
		t.Fatal(err)
	}
	if _, err := png.Decode(bytes.NewReader(img)); err != nil {
		t.Errorf("pushed image is not a PNG: %v", err)
	}

	// The same job again before the interval: no push.
	p.MaybePush(t.Context(), target, "benchy.bgcode", "/usb/BENCHY~1.BGC", discardLogger())
	waitIdle(t, target)
	if n := loki.count(); n != 1 {
		t.Errorf("%d pushes after a repeat within the interval, want 1", n)
	}

	// After the interval: a push.
	now = now.Add(lokiRepushInterval)
	p.MaybePush(t.Context(), target, "benchy.bgcode", "/usb/BENCHY~1.BGC", discardLogger())
	waitFor(t, p, "success", 2)
}

func TestLokiPushErrors(t *testing.T) {
	t.Parallel()
	f := newFakePrusaLink(t, false)
	loki := &fakeLoki{code: http.StatusBadRequest}
	ls := httptest.NewServer(loki)
	t.Cleanup(ls.Close)
	p := NewLokiPusher(ls.URL, prometheus.NewRegistry())
	target := &Target{Client: NewClient(f.printer(), NewTransport(), nil)}

	// No thumbnail (404).
	p.MaybePush(t.Context(), target, "a", "/usb/A.BGC", discardLogger())
	waitFor(t, p, "thumbnail_error", 1)
	waitIdle(t, target)

	// Not an image.
	f.setRaw("/thumb/l/usb/B.BGC", "not a png")
	p.MaybePush(t.Context(), target, "b", "/usb/B.BGC", discardLogger())
	waitFor(t, p, "thumbnail_error", 2)
	waitIdle(t, target)

	// Loki rejects the push. A failed push is tried again at the next scrape.
	f.setRaw("/thumb/l/usb/C.BGC", string(testPNG(t)))
	p.MaybePush(t.Context(), target, "c", "/usb/C.BGC", discardLogger())
	waitFor(t, p, "push_error", 1)
	waitIdle(t, target)
	p.MaybePush(t.Context(), target, "c", "/usb/C.BGC", discardLogger())
	waitFor(t, p, "push_error", 2)
}
