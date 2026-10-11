package render

import (
	"fmt"
	"testing"
)

func BenchmarkRenderCold(b *testing.B) {
	for i, f := range fixtures() {
		for _, w := range []int{60, 120} {
			b.Run(fmt.Sprintf("fx%d-%dB/w%d", i, len(f.Body), w), func(b *testing.B) {
				j := Job{Content: f.Body, Width: w, Style: Dark}
				for b.Loop() {
					if _, err := Render(j); err != nil {
						b.Fatal(err)
					}
				}
			})
		}
	}
}

func BenchmarkRender48KB(b *testing.B) {
	body := largeBody()
	for _, st := range styles {
		b.Run(fmt.Sprintf("%dB/w80/%v", len(body), st), func(b *testing.B) {
			b.ReportAllocs()
			j := Job{Content: body, Width: 80, Style: st}
			for b.Loop() {
				if _, err := Render(j); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkCacheHit(b *testing.B) {
	svc := NewService(DefaultCacheBytes)
	slot := NewSlot(svc)
	j := Job{RecordID: "r", Revision: 1, Content: largeBody(), Width: 120, Style: Dark}
	if r := svc.Run(j); r.Err != nil {
		b.Fatal(r.Err)
	}
	b.ReportAllocs()
	for b.Loop() {
		if d, _ := slot.Request(j); d == nil { // key hash + lookup
			b.Fatal("miss")
		}
	}
}
