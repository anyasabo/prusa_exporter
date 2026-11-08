package udp

import (
	"reflect"
	"testing"
	"time"
)

func TestParsePoint(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		line  string
		want  *point
		isErr bool
	}{
		{
			name: "float",
			line: "temp_noz v=215.500000 123",
			want: &point{Name: "temp_noz", Tags: map[string]string{}, Fields: map[string]any{"v": 215.5}, Delta: 123, HasDelta: true},
		},
		{
			name: "integer with tags",
			line: "fan,fan=print,t=1 rpm=1500i -5",
			want: &point{Name: "fan", Tags: map[string]string{"fan": "print", "t": "1"}, Fields: map[string]any{"rpm": int64(1500)}, Delta: -5, HasDelta: true},
		},
		{
			name: "event",
			line: "probe_start v=T 0",
			want: &point{Name: "probe_start", Tags: map[string]string{}, Fields: map[string]any{"v": true}, HasDelta: true},
		},
		{
			name: "one and zero are numbers",
			line: "phxy_orig c=1,p=0 0",
			want: &point{Name: "phxy_orig", Tags: map[string]string{}, Fields: map[string]any{"c": 1.0, "p": 0.0}, HasDelta: true},
		},
		{
			name: "string with escapes, comma and space",
			line: `print_filename v="a \"b\", c\\d" 7`,
			want: &point{Name: "print_filename", Tags: map[string]string{}, Fields: map[string]any{"v": `a "b", c\d`}, Delta: 7, HasDelta: true},
		},
		{
			name: "quoted tag value",
			line: `phxy_home,t="h" h=1i 0`,
			want: &point{Name: "phxy_home", Tags: map[string]string{"t": "h"}, Fields: map[string]any{"h": int64(1)}, HasDelta: true},
		},
		{
			name: "no time",
			line: "temp_bed v=60",
			want: &point{Name: "temp_bed", Tags: map[string]string{}, Fields: map[string]any{"v": 60.0}},
		},
		{
			name: "time that is not a number",
			line: "temp_bed v=60 soon",
			want: &point{Name: "temp_bed", Tags: map[string]string{}, Fields: map[string]any{"v": 60.0}},
		},
		{
			name: "error point",
			line: `tmc_read error="value_too_long" 4`,
			want: &point{Name: "tmc_read", Tags: map[string]string{}, Fields: map[string]any{"error": "value_too_long"}, Delta: 4, HasDelta: true},
		},
		{name: "no fields", line: "temperature,sensor=nozzle", isErr: true},
		{name: "too many parts", line: "t v=1 2 3", isErr: true},
		{name: "tag without value", line: "t,sensor v=1 0", isErr: true},
		{name: "field without value", line: "t v 0", isErr: true},
		{name: "empty name", line: ",a=b v=1 0", isErr: true},
		{name: "raw output", line: "0x1234", isErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := parsePoint(tt.line)
			if tt.isErr {
				if err == nil {
					t.Fatalf("parsePoint(%q) = %+v, want an error", tt.line, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("parsePoint(%q) error = %v", tt.line, err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("parsePoint(%q) =\n%+v\nwant\n%+v", tt.line, got, tt.want)
			}
		})
	}
}

func TestUnquote(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]string{
		`"h"`: "h", `h`: "h", `""`: "", `"`: `"`, `"a\\b"`: `a\b`, `"a\"b"`: `a"b`, `"a\"`: `a\`,
	} {
		if got := unquote(in); got != want {
			t.Errorf("unquote(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSetSampleTimes(t *testing.T) {
	t.Parallel()
	received := time.Unix(1000, 0)
	points := []*point{
		{Delta: 0, HasDelta: true},
		{Delta: 250_000, HasDelta: true},
		{},
		{Delta: 1_000_000, HasDelta: true},
	}
	setSampleTimes(points, received)
	want := []time.Time{received.Add(-time.Second), received.Add(-750 * time.Millisecond), {}, received}
	for i, p := range points {
		if !p.Time.Equal(want[i]) {
			t.Errorf("point %d: time %v, want %v", i, p.Time, want[i])
		}
	}
}

func FuzzParsePoint(f *testing.F) {
	for _, s := range []string{
		"temp_noz v=215.5 0", "fan,fan=print rpm=1500i 100", `s v="a \"b\"" 1`, "e v=T", `x,t="q" a=1,b=2 -3`, `\`,
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, line string) {
		p, err := parsePoint(line)
		if err != nil {
			return
		}
		if p.Name == "" || len(p.Fields) == 0 {
			t.Fatalf("parsePoint(%q) = %+v without error, but no name or no fields", line, p)
		}
	})
}
