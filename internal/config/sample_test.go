package config

import "testing"

// TestSampleConfig checks that the sample file in docs is valid.
func TestSampleConfig(t *testing.T) {
	t.Parallel()
	c, err := Load("../../docs/config/prusa.yml")
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Printers) != 1 {
		t.Errorf("sample has %d printers, want 1", len(c.Printers))
	}
}
