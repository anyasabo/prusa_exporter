package udp

import (
	"fmt"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"
)

// benchDatagram is about 1 KB with 20 points, like a full firmware buffer.
func benchDatagram() []byte {
	var b strings.Builder
	b.WriteString("<14>1 - 10:9c:70:00:00:01 buddy - - - msg=1,tm=123456,v=4 ")
	for i := range 20 {
		switch i % 4 {
		case 0:
			fmt.Fprintf(&b, "temp_noz v=215.%d %d\n", i, i*1000)
		case 1:
			fmt.Fprintf(&b, "fan,fan=print rpm=%di,pwm=128i %d\n", 1500+i, i*1000)
		case 2:
			fmt.Fprintf(&b, "loadcell,t=%d v=%d.5 %d\n", i%3, i, i*1000)
		default:
			fmt.Fprintf(&b, "heap free=%di,total=%di %d\n", 50000+i, 131072, i*1000)
		}
	}
	return []byte(b.String())
}

func BenchmarkHandle(b *testing.B) {
	c, _ := newTestCollector(b, Options{EventMetrics: testEvents})
	data := benchDatagram()
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	for b.Loop() {
		c.Handle(data, printer, t0)
	}
}

func BenchmarkCollect(b *testing.B) {
	c, _ := newTestCollector(b, Options{})
	c.Handle(benchDatagram(), printer, t0)
	b.ReportAllocs()
	for b.Loop() {
		testutil.CollectAndCount(c)
	}
}
