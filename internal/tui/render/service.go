package render

import (
	"container/list"
	"sync"
)

// DefaultCacheBytes is the default render cache limit (32 MiB). A 48 KB body
// costs about 0.6 MB at 80 columns and 1.5 MB at 200, while typical bodies of
// a few KB cost tens of KB, so this keeps a few hundred recent renders.
const DefaultCacheBytes = 32 << 20

// Cache is a least-recently-used cache of rendered documents bounded by
// their total Size. It is safe for concurrent use.
type Cache struct {
	mu     sync.Mutex
	limit  int64
	bytes  int64
	order  *list.List // front = most recently used; values are *entry
	byKey  map[Key]*list.Element
	counts CacheStats
}

type entry struct {
	key Key
	doc *Doc
}

// CacheStats describes a cache's contents and history.
type CacheStats struct {
	Entries                 int
	Bytes, Limit            int64
	Hits, Misses, Evictions uint64
}

// NewCache returns an empty cache holding at most limit bytes.
func NewCache(limit int64) *Cache {
	return &Cache{limit: limit, order: list.New(), byKey: map[Key]*list.Element{}}
}

// Get returns the cached document for k and marks it recently used.
func (c *Cache) Get(k Key) (*Doc, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if e, ok := c.byKey[k]; ok {
		c.order.MoveToFront(e)
		c.counts.Hits++
		return e.Value.(*entry).doc, true
	}
	c.counts.Misses++
	return nil, false
}

// Put stores d under k, evicting least recently used documents until the
// cache fits its limit. A document larger than the whole limit is not stored.
func (c *Cache) Put(k Key, d *Doc) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if d.Size() > c.limit {
		return
	}
	if e, ok := c.byKey[k]; ok {
		c.bytes -= e.Value.(*entry).doc.Size()
		c.order.Remove(e)
		delete(c.byKey, k)
	}
	for c.bytes+d.Size() > c.limit {
		old := c.order.Back()
		c.order.Remove(old)
		ev := old.Value.(*entry)
		delete(c.byKey, ev.key)
		c.bytes -= ev.doc.Size()
		c.counts.Evictions++
	}
	c.byKey[k] = c.order.PushFront(&entry{k, d})
	c.bytes += d.Size()
}

// Stats returns the cache's current statistics.
func (c *Cache) Stats() CacheStats {
	c.mu.Lock()
	defer c.mu.Unlock()
	s := c.counts
	s.Entries, s.Bytes, s.Limit = len(c.byKey), c.bytes, c.limit
	return s
}

// Result is the outcome of a render job.
type Result struct {
	Key Key
	Doc *Doc
	Err error
}

// Service renders jobs into a shared cache. Every render builds its own
// renderers; concurrent requests for one key share a single render. It is
// safe for concurrent use.
type Service struct {
	cache   *Cache
	mu      sync.Mutex
	flights map[Key]*flight
}

type flight struct {
	done chan struct{}
	res  Result
}

// NewService returns a service whose cache holds at most cacheBytes.
func NewService(cacheBytes int64) *Service {
	return &Service{cache: NewCache(cacheBytes), flights: map[Key]*flight{}}
}

// Cache returns the service's cache.
func (s *Service) Cache() *Cache { return s.cache }

// Lookup returns a cached render for j without rendering.
func (s *Service) Lookup(j Job) (*Doc, bool) { return s.cache.Get(j.Key()) }

// Run returns the render for j: from the cache, by joining a render of the
// same key already in progress, or by rendering it. Successful results are
// cached whether or not anyone still wants them. Run blocks; call it off the
// UI goroutine (for example inside a tea.Cmd).
func (s *Service) Run(j Job) Result {
	k := j.Key()
	if d, ok := s.cache.Get(k); ok {
		return Result{Key: k, Doc: d}
	}
	s.mu.Lock()
	if f, ok := s.flights[k]; ok {
		s.mu.Unlock()
		<-f.done
		return f.res
	}
	f := &flight{done: make(chan struct{})}
	s.flights[k] = f
	s.mu.Unlock()

	d, err := Render(j)
	f.res = Result{Key: k, Doc: d, Err: err}
	if err == nil {
		s.cache.Put(k, d)
	}
	s.mu.Lock()
	delete(s.flights, k)
	s.mu.Unlock()
	close(f.done)
	return f.res
}

// Slot tracks the render one display pane wants. It belongs to the UI
// goroutine (a Bubble Tea model) and is not safe for concurrent use; the
// functions it returns are.
type Slot struct {
	svc     *Service
	want    Key
	wanted  bool
	pending map[Key]bool
}

// NewSlot returns a slot rendering through svc.
func NewSlot(svc *Service) *Slot { return &Slot{svc: svc, pending: map[Key]bool{}} }

// Request makes j the wanted render. On a cache hit it returns the document
// and a nil function. On a miss it returns a function that renders j and may
// run on any goroutine (wrap it in a tea.Cmd and pass its Result back to
// Accept), or nil when a render for that key is already pending.
func (s *Slot) Request(j Job) (*Doc, func() Result) {
	k := j.Key()
	s.want, s.wanted = k, true
	if d, ok := s.svc.cache.Get(k); ok {
		return d, nil
	}
	if s.pending[k] {
		return nil, nil
	}
	s.pending[k] = true
	return nil, func() Result { return s.svc.Run(j) }
}

// Accept records a finished render and reports whether it is the one the
// pane currently wants. A stale result is not for display, but the service
// has already cached it.
func (s *Slot) Accept(r Result) bool {
	delete(s.pending, r.Key)
	return s.wanted && r.Key == s.want
}

// Want returns the key of the render the pane wants, if any.
func (s *Slot) Want() (Key, bool) { return s.want, s.wanted }
