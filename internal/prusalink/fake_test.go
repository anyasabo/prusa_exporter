package prusalink

import (
	"crypto/md5" //nolint:gosec // HTTP digest uses MD5.
	"embed"
	"encoding/hex"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/anyasabo/prusa_exporter/internal/config"
)

const (
	testUser   = "maker"
	testPass   = "secret"
	testAPIKey = "key123"
	testRealm  = "Printer API"
	testNonce  = "dcd98b7102dd2f0e8b11d0f600bfb0c093"
)

//go:embed testdata/*.json
var testdata embed.FS

// fakePrusaLink is a PrusaLink server for tests. It checks the digest or the API key.
type fakePrusaLink struct {
	t      *testing.T
	server *httptest.Server
	digest bool

	mu sync.Mutex
	// files maps a path to a fixture file in testdata. A missing path is 404.
	files map[string]string
	// codes maps a path to a status code to return instead of the fixture.
	codes map[string]int
	// raw maps a path to a body to return instead of the fixture.
	raw map[string]string
	// requests counts the authenticated requests by "METHOD path".
	requests map[string]int
	// challenges counts the 401 responses.
	challenges int
	// bodies keeps the request bodies by "METHOD path".
	bodies map[string]string
}

func newFakePrusaLink(t *testing.T, digest bool) *fakePrusaLink {
	t.Helper()
	f := &fakePrusaLink{
		t:      t,
		digest: digest,
		files: map[string]string{
			"/api/v1/status": "status_printing.json",
			"/api/v1/info":   "info.json",
			"/api/version":   "version.json",
			"/api/printer":   "printer.json",
			"/api/job":       "job.json",
		},
		codes:    map[string]int{},
		raw:      map[string]string{},
		requests: map[string]int{},
		bodies:   map[string]string{},
	}
	f.server = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.server.Close)
	return f
}

func (f *fakePrusaLink) printer() config.Printer {
	p := config.Printer{Address: strings.TrimPrefix(f.server.URL, "http://"), Name: "Test", Type: "COREONEL"}
	if f.digest {
		p.Username, p.Password = testUser, testPass
	} else {
		p.APIKey = testAPIKey
	}
	return p
}

func (f *fakePrusaLink) set(path, file string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.files[path] = file
}

func (f *fakePrusaLink) setCode(path string, code int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.codes[path] = code
}

func (f *fakePrusaLink) setRaw(path, body string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.raw[path] = body
}

func (f *fakePrusaLink) count(key string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.requests[key]
}

var digestParam = regexp.MustCompile(`(\w+)="?([^",]*)"?`)

func md5hex(s string) string {
	h := md5.Sum([]byte(s)) //nolint:gosec // HTTP digest uses MD5.
	return hex.EncodeToString(h[:])
}

func (f *fakePrusaLink) authorized(r *http.Request) bool {
	if !f.digest {
		return r.Header.Get("X-Api-Key") == testAPIKey
	}
	auth, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Digest ")
	if !ok {
		return false
	}
	p := map[string]string{}
	for _, m := range digestParam.FindAllStringSubmatch(auth, -1) {
		p[m[1]] = m[2]
	}
	ha1 := md5hex(testUser + ":" + testRealm + ":" + testPass)
	ha2 := md5hex(r.Method + ":" + p["uri"])
	want := md5hex(strings.Join([]string{ha1, testNonce, p["nc"], p["cnonce"], p["qop"], ha2}, ":"))
	return p["username"] == testUser && p["nonce"] == testNonce && p["response"] == want
}

func (f *fakePrusaLink) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.authorized(r) {
		f.challenges++
		if f.digest {
			w.Header().Set("WWW-Authenticate", `Digest realm="`+testRealm+`", nonce="`+testNonce+`", qop="auth", algorithm=MD5`)
		}
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	key := r.Method + " " + r.URL.Path
	f.requests[key]++
	body, _ := io.ReadAll(r.Body)
	f.bodies[key] = string(body)

	if code, ok := f.codes[r.URL.Path]; ok {
		w.WriteHeader(code)
		return
	}
	if body, ok := f.raw[r.URL.Path]; ok {
		_, _ = w.Write([]byte(body))
		return
	}
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	name, ok := f.files[r.URL.Path]
	if !ok {
		http.NotFound(w, r)
		return
	}
	data, err := testdata.ReadFile("testdata/" + name)
	if err != nil {
		f.t.Errorf("fixture: %v", err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(data)
}

func discardLogger() *slog.Logger {
	return slog.New(slog.DiscardHandler)
}

func newFakePrusaLinkPrinter(url string) config.Printer {
	return config.Printer{Address: strings.TrimPrefix(url, "http://"), APIKey: testAPIKey, Name: "Slow", Type: "MK4"}
}

// MetricNamesExcept returns the metric names of the collector without the names in skip.
func MetricNamesExcept(skip ...string) []string {
	var r []string
	for _, n := range MetricNames() {
		if !slices.Contains(skip, n) {
			r = append(r, n)
		}
	}
	return r
}
