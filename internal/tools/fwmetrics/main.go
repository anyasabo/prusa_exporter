// Command fwmetrics reads the metric definitions from a Prusa Buddy firmware ELF file
// and writes them as JSON. The firmware puts each METRIC_DEF in its own section with
// the symbol "metric_<name>" (include/common/metric.h), so the list is exact for the
// build: it has only the metrics that the linker kept for that printer model.
//
//	go run ./internal/tools/fwmetrics -elf build/coreonel_release_boot/firmware -model coreonel -firmware 6.5.0 > docs/firmware/coreonel.json
package main

import (
	"bytes"
	"debug/elf"
	"encoding/binary"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"slices"
	"strings"
)

// Metric is one METRIC_DEF of the firmware.
type Metric struct {
	Name          string `json:"name"`
	Type          string `json:"type"`
	MinIntervalMS uint16 `json:"min_interval_ms"`
	Enabled       bool   `json:"enabled_by_default"`
}

// Reference is the output of the command.
type Reference struct {
	Model    string   `json:"model"`
	Firmware string   `json:"firmware"`
	Metrics  []Metric `json:"metrics"`
}

// metricSize is sizeof(metric_t) on the 32-bit ARM target.
const metricSize = 12

var typeNames = map[uint16]string{0: "event", 1: "float", 2: "integer", 3: "string", 4: "custom"}

func main() {
	path := flag.String("elf", "", "path of the firmware ELF file")
	model := flag.String("model", "", "printer model of the build, for example coreonel")
	firmware := flag.String("firmware", "", "firmware version of the build")
	flag.Parse()
	if *path == "" || *model == "" {
		fmt.Fprintln(os.Stderr, "usage: fwmetrics -elf FILE -model MODEL [-firmware VERSION]")
		os.Exit(2)
	}
	metrics, err := read(*path)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(Reference{Model: *model, Firmware: *firmware, Metrics: metrics}); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func read(path string) ([]Metric, error) {
	f, err := elf.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open ELF: %w", err)
	}
	defer f.Close()
	if f.Class != elf.ELFCLASS32 || f.Data != elf.ELFDATA2LSB {
		return nil, errors.New("not a 32-bit little-endian ELF file")
	}
	symbols, err := f.Symbols()
	if err != nil {
		return nil, fmt.Errorf("read symbols: %w", err)
	}

	var metrics []Metric
	for _, s := range symbols {
		name, ok := strings.CutPrefix(s.Name, "metric_")
		if !ok || s.Size != metricSize {
			continue
		}
		// LTO can add a suffix, for example "metric_touch_evt.lto_priv.0".
		name, _, _ = strings.Cut(name, ".")
		raw, err := readAt(f, s.Value, metricSize)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", s.Name, err)
		}
		namePtr := binary.LittleEndian.Uint32(raw[0:4])
		bits := binary.LittleEndian.Uint16(raw[10:12])
		defName, err := readString(f, uint64(namePtr))
		if err != nil {
			return nil, fmt.Errorf("%s: name: %w", s.Name, err)
		}
		// The symbol name and the name in the definition must be the same, else this
		// symbol is not a metric_t.
		if defName != name {
			continue
		}
		typ, ok := typeNames[bits&0x7]
		if !ok {
			return nil, fmt.Errorf("%s: unknown type %d", s.Name, bits&0x7)
		}
		metrics = append(metrics, Metric{
			Name:          name,
			Type:          typ,
			MinIntervalMS: binary.LittleEndian.Uint16(raw[8:10]),
			Enabled:       bits&0x8 != 0,
		})
	}
	if len(metrics) == 0 {
		return nil, errors.New("no metric definitions found")
	}
	slices.SortFunc(metrics, func(a, b Metric) int { return strings.Compare(a.Name, b.Name) })
	return metrics, nil
}

// sectionData caches the data of each section.
var sectionData = map[*elf.Section][]byte{}

// readAt returns n bytes at the virtual address addr, from the section with the
// initial data.
func readAt(f *elf.File, addr, n uint64) ([]byte, error) {
	for _, sec := range f.Sections {
		if sec.Type != elf.SHT_PROGBITS || addr < sec.Addr || addr+n > sec.Addr+sec.Size {
			continue
		}
		data, ok := sectionData[sec]
		if !ok {
			var err error
			if data, err = sec.Data(); err != nil {
				return nil, fmt.Errorf("read section %s: %w", sec.Name, err)
			}
			sectionData[sec] = data
		}
		off := addr - sec.Addr
		return data[off : off+n], nil
	}
	return nil, fmt.Errorf("no section has the address %#x", addr)
}

// readString returns the zero-terminated string at addr.
func readString(f *elf.File, addr uint64) (string, error) {
	b, err := readAt(f, addr, 64)
	if err != nil {
		// The string can be at the end of a section.
		if b, err = readAt(f, addr, 1); err != nil {
			return "", err
		}
	}
	if i := bytes.IndexByte(b, 0); i >= 0 {
		return string(b[:i]), nil
	}
	return "", errors.New("string is longer than 64 bytes")
}
