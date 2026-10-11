package storage

import (
	"sync"
	"testing"
	"time"
)

func TestClockNeverGoesBackwards(t *testing.T) {
	base := time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)
	walls := []time.Time{base.Add(5 * time.Second), base.Add(time.Second), base.Add(5 * time.Second), base.Add(7 * time.Second), base}
	want := []time.Time{base.Add(5 * time.Second), base.Add(5 * time.Second), base.Add(5 * time.Second), base.Add(7 * time.Second), base.Add(7 * time.Second)}
	i := 0
	c := NewClock(func() time.Time { v := walls[i]; i++; return v })
	for n, w := range want {
		if got := c.Now(); !got.Equal(w) || got.Location() != time.UTC {
			t.Fatalf("sample %d = %v, want %v in UTC", n, got, w)
		}
	}
}

func TestClockSamplesAreUTCWallTimes(t *testing.T) {
	local := time.FixedZone("x", 3600)
	c := NewClock(func() time.Time { return time.Date(2026, 10, 11, 13, 0, 0, 0, local) })
	got := c.Now()
	if got.Location() != time.UTC || got.Hour() != 12 {
		t.Fatalf("sample %v is not the UTC wall time", got)
	}
	if got != got.Round(0) {
		t.Fatal("sample keeps a monotonic clock reading")
	}
}

func TestClockConcurrentSamplesAreMonotonic(t *testing.T) {
	c := NewClock(time.Now)
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			last := c.Now()
			for range 1000 {
				v := c.Now()
				if v.Before(last) {
					t.Errorf("sample %v before %v", v, last)
					return
				}
				last = v
			}
		}()
	}
	wg.Wait()
}
