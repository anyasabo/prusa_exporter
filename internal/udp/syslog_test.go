package udp

import (
	"reflect"
	"testing"
)

func TestParseMessage(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		data  string
		want  message
		isErr bool
	}{
		{
			name: "firmware message",
			data: "<14>1 - 10:9c:70:00:00:01 buddy - - - msg=12,tm=123456,v=4 temp_noz v=215.5 0\nfan rpm=1i 10\n",
			want: message{Hostname: "10:9c:70:00:00:01", Points: []string{"temp_noz v=215.5 0", "fan rpm=1i 10"}},
		},
		{
			name: "header only",
			data: "<14>1 - mac buddy - - - msg=1,tm=1,v=4 ",
			want: message{Hostname: "mac"},
		},
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
			got, err := parseMessage(tt.data)
			if tt.isErr {
				if err == nil {
					t.Fatalf("parseMessage(%q) = %+v, want an error", tt.data, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseMessage(%q) error = %v", tt.data, err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("parseMessage(%q) = %+v, want %+v", tt.data, got, tt.want)
			}
		})
	}
}

func FuzzParseMessage(f *testing.F) {
	f.Add("<14>1 - mac buddy - - - msg=1,tm=2,v=4 temp_noz v=215.5 0\nfan rpm=1i 10\n")
	f.Add("<14>1 - - - - - -")
	f.Fuzz(func(t *testing.T, data string) {
		m, err := parseMessage(data)
		if err != nil {
			return
		}
		if m.Hostname == "" {
			t.Fatalf("parseMessage(%q) has no hostname but no error", data)
		}
		for _, line := range m.Points {
			if line == "" {
				t.Fatalf("parseMessage(%q) has an empty point", data)
			}
			_, _ = parsePoint(line)
		}
	})
}
