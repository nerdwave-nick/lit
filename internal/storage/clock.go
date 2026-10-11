package storage

import (
	"sync"
	"time"
)

// Clock is the process clock for request times: it never goes backwards, so
// each sample is max(last issued, wall clock). Samples are UTC wall times
// without Go's monotonic reading, so a wall-clock step back cannot reorder
// formatted timestamps.
type Clock struct {
	mu   sync.Mutex
	wall func() time.Time
	last time.Time
}

// NewClock returns a clock over the wall time source, normally time.Now.
func NewClock(wall func() time.Time) *Clock { return &Clock{wall: wall} }

// Now returns the next sample: the later of the last sample and the wall clock.
func (c *Clock) Now() time.Time {
	v := c.wall().UTC() // UTC also strips the monotonic reading.
	c.mu.Lock()
	defer c.mu.Unlock()
	if v.Before(c.last) {
		return c.last
	}
	c.last = v
	return v
}
