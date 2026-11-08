package udp

import (
	"reflect"
	"testing"
)

func TestParseMessage(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		data     string
		hostname string
		lines    []string
		isErr    bool
	}{
		{
			name:     "firmware message",
			data:     "<14>1 - 10:9c:70:00:00:01 buddy - - - msg=12,tm=123456,v=4 temp_noz v=215.5 0\nfan rpm=1i 10\n",
			hostname: "10:9c:70:00:00:01",
			lines:    []string{"temp_noz v=215.5 0", "fan rpm=1i 10"},
		},
		{
			name:     "empty lines",
			data:     "<14>1 - mac buddy - - - msg=1,tm=1,v=4 a v=1\n\n\nb v=2",
			hostname: "mac",
			lines:    []string{"a v=1", "b v=2"},
		},
		{name: "header only", data: "<14>1 - mac buddy - - - msg=1,tm=1,v=4 ", hostname: "mac"},
		{name: "empty", data: "", isErr: true},
		{name: "no PRI", data: "14>1 - mac buddy - - - msg=1 x v=1", isErr: true},
		{name: "version 0", data: "<14>0 - mac buddy - - - msg=1 x v=1", isErr: true},
		{name: "short header", data: "<14>1 - mac buddy", isErr: true},
		{name: "structured data", data: "<14>1 - mac buddy - - [a b=\"c\"] msg=1 x v=1", isErr: true},
		{name: "no hostname", data: "<14>1 - - buddy - - - msg=1 x v=1", isErr: true},
		{name: "no msg header", data: "<14>1 - mac buddy - - - x v=1 0", isErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			hostname, body, err := parseMessage(tt.data)
			if tt.isErr {
				if err == nil {
					t.Fatalf("parseMessage(%q) = %q, want an error", tt.data, hostname)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseMessage(%q) error = %v", tt.data, err)
			}
			var lines []string
			for line, rest := nextLine(body); line != ""; line, rest = nextLine(rest) {
				lines = append(lines, line)
			}
			if hostname != tt.hostname || !reflect.DeepEqual(lines, tt.lines) {
				t.Errorf("parseMessage(%q) = %q, %q, want %q, %q", tt.data, hostname, lines, tt.hostname, tt.lines)
			}
		})
	}
}

func FuzzParseMessage(f *testing.F) {
	f.Add("<14>1 - mac buddy - - - msg=1,tm=2,v=4 temp_noz v=215.5 0\nfan rpm=1i 10\n")
	f.Add("<14>1 - - - - - -")
	f.Fuzz(func(t *testing.T, data string) {
		hostname, body, err := parseMessage(data)
		if err != nil {
			return
		}
		if hostname == "" {
			t.Fatalf("parseMessage(%q) has no hostname but no error", data)
		}
		var s scratch
		for line, rest := nextLine(body); line != ""; line, rest = nextLine(rest) {
			_, _ = s.parsePoint(line)
		}
	})
}
