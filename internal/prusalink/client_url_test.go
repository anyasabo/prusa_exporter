package prusalink

import (
	"testing"

	"github.com/anyasabo/prusa_exporter/v3/internal/config"
)

// TestClientURL checks the URL for each address form. An IPv6 address needs brackets.
func TestClientURL(t *testing.T) {
	t.Parallel()
	for addr, want := range map[string]string{
		"192.168.1.10":      "http://192.168.1.10/api/v1/status",
		"192.168.1.10:8080": "http://192.168.1.10:8080/api/v1/status",
		"printer.lan":       "http://printer.lan/api/v1/status",
		"fd00::10":          "http://[fd00::10]/api/v1/status",
		"[fd00::10]:80":     "http://[fd00::10]:80/api/v1/status",
	} {
		c := &Client{Printer: config.Printer{Address: addr}}
		if got := c.url("/api/v1/status"); got != want {
			t.Errorf("url(%q) = %q, want %q", addr, got, want)
		}
	}
}
