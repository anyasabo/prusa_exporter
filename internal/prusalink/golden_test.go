package prusalink

import (
	"bytes"
	"flag"
	"io/fs"
	"os"
	"path"
	"strings"
	"testing"

	"github.com/prometheus/common/expfmt"
)

var update = flag.Bool("update", false, "write the golden files again")

// TestCaptures scrapes each capture directory in testdata (real PrusaLink responses)
// and compares the output with metrics.prom in the directory. To write the files
// again: go test -run TestCaptures ./internal/prusalink -args -update
func TestCaptures(t *testing.T) {
	t.Parallel()
	dirs, err := fs.Glob(testdata, "testdata/*/status.json")
	if err != nil || len(dirs) == 0 {
		t.Fatalf("no capture directories: %v", err)
	}
	endpoints := map[string]string{
		"/api/v1/status": "status.json",
		"/api/v1/info":   "info.json",
		"/api/version":   "version.json",
		"/api/printer":   "printer.json",
		"/api/job":       "job.json",
	}
	for _, status := range dirs {
		dir := path.Dir(status)
		t.Run(path.Base(dir), func(t *testing.T) {
			t.Parallel()
			f := newFakePrusaLink(t, false)
			for endpoint, file := range endpoints {
				f.set(endpoint, path.Join(path.Base(dir), file))
			}
			mfs, err := gather(t, newTestExporter(t, f, Options{})).Gather()
			if err != nil {
				t.Fatal(err)
			}
			var b bytes.Buffer
			enc := expfmt.NewEncoder(&b, expfmt.NewFormat(expfmt.TypeTextPlain))
			for _, mf := range mfs {
				if mf.GetName() == "prusa_scrape_duration_seconds" {
					continue // not the same in each run
				}
				if err := enc.Encode(mf); err != nil {
					t.Fatal(err)
				}
			}
			got := strings.ReplaceAll(b.String(), f.printer().Address, "PRINTER")

			golden := path.Join(dir, "metrics.prom")
			if *update {
				if err := os.WriteFile(golden, []byte(got), 0o600); err != nil {
					t.Fatal(err)
				}
				return
			}
			want, err := testdata.ReadFile(golden)
			if err != nil {
				t.Fatalf("%v. Run the test with -update.", err)
			}
			if string(want) != got {
				t.Errorf("the output is not %s. If the change is correct, run the test with -update.\n\ngot:\n%s", golden, got)
			}
		})
	}
}
