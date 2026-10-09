package edit

import (
	"crypto/sha256"
	"os"
	"sync"
	"time"
)

// WatchEvery is how often a watched draft file is checked (Proposal).
const WatchEvery = 500 * time.Millisecond

// Watch checks the file at path every interval, on its own goroutine, and
// calls changed after each change of its content (including its removal or
// re-creation). It polls, so it works on every platform and file system and
// with editors that replace the file when saving. stop ends it and returns
// once the goroutine has exited; it may be called more than once.
func Watch(path string, every time.Duration, changed func()) (stop func()) {
	done, quit := make(chan struct{}), make(chan struct{})
	last := stamp(path)
	go func() {
		defer close(done)
		t := time.NewTicker(every)
		defer t.Stop()
		for {
			select {
			case <-quit:
				return
			case <-t.C:
			}
			if now := stamp(path); now != last {
				last = now
				changed()
			}
		}
	}()
	var once sync.Once
	return func() {
		once.Do(func() { close(quit) })
		<-done
	}
}

// stamp identifies the file's content: its hash, or "" when it cannot be
// read.
func stamp(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(b)
	return string(sum[:])
}
