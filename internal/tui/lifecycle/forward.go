package lifecycle

import (
	"sync"

	tea "charm.land/bubbletea/v2"
)

// forwarder is the only caller of Program.Send. Posting never blocks: messages
// are coalesced by key and delivered in order of first posting by one
// goroutine, which may itself block for as long as the program is suspended.
type forwarder struct {
	mu    sync.Mutex
	queue map[string]tea.Msg
	order []string
	wake  chan struct{}
	once  sync.Once
}

func (f *forwarder) init() {
	f.queue = map[string]tea.Msg{}
	f.wake = make(chan struct{}, 1)
}

func (f *forwarder) post(key string, msg tea.Msg) {
	f.mu.Lock()
	if _, ok := f.queue[key]; !ok {
		f.order = append(f.order, key)
	}
	f.queue[key] = msg
	f.mu.Unlock()
	select {
	case f.wake <- struct{}{}:
	default: // a wake-up is pending; it delivers the latest messages
	}
}

// start delivers to p, once.
func (f *forwarder) start(p *tea.Program) {
	f.once.Do(func() {
		go func() {
			for range f.wake {
				f.mu.Lock()
				batch := make([]tea.Msg, 0, len(f.order))
				for _, k := range f.order {
					batch = append(batch, f.queue[k])
				}
				f.queue, f.order = map[string]tea.Msg{}, nil
				f.mu.Unlock()
				for _, msg := range batch {
					p.Send(msg) // a no-op once the program has ended
				}
			}
		}()
	})
}
