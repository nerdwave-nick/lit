package render

import (
	"sync"
	"testing"
)

func doc(lines ...string) *Doc {
	d := &Doc{Blocks: []Block{{Kind: Prose, Lines: lines}}}
	d.size = docSize(d)
	return d
}

func TestCacheIsBoundedByBytesLRU(t *testing.T) {
	k := func(i int) Key { return Job{RecordID: "r", Revision: int64(i)}.Key() }
	one := doc(string(make([]byte, 1000)))
	c := NewCache(3 * one.Size())
	for i := range 3 {
		c.Put(k(i), one)
	}
	if _, ok := c.Get(k(0)); !ok { // k(0) becomes most recently used
		t.Fatal("entry 0 missing before the limit was reached")
	}
	c.Put(k(3), one) // evicts k(1), the least recently used
	if _, ok := c.Get(k(1)); ok {
		t.Error("least recently used entry survived eviction")
	}
	for _, i := range []int{0, 2, 3} {
		if _, ok := c.Get(k(i)); !ok {
			t.Errorf("entry %d evicted", i)
		}
	}
	st := c.Stats()
	if st.Bytes > st.Limit || st.Entries != 3 || st.Evictions != 1 {
		t.Errorf("stats = %+v", st)
	}
	huge := doc(string(make([]byte, 10000)))
	c.Put(k(9), huge)
	if _, ok := c.Get(k(9)); ok || c.Stats().Entries != 3 {
		t.Error("an entry larger than the whole cache was stored")
	}
}

func TestDocSizeCountsRenderedBytes(t *testing.T) {
	d := mustRender(t, Job{Content: largeBody(), Width: 80, Style: Dark})
	var raw int64
	for _, b := range d.Blocks {
		for _, l := range b.Lines {
			raw += int64(len(l))
		}
	}
	if d.Size() < raw {
		t.Errorf("Size %d < rendered bytes %d", d.Size(), raw)
	}
}

func TestSlotDropsStaleResultsButKeepsThemCached(t *testing.T) {
	svc := NewService(DefaultCacheBytes)
	slot := NewSlot(svc)
	body := fixtures()[1].Body
	j80 := Job{RecordID: "r", Revision: 1, Content: body, Width: 80, Style: Dark}
	j120 := j80
	j120.Width = 120

	d, run80 := slot.Request(j80)
	if d != nil || run80 == nil {
		t.Fatal("first request should start a render")
	}
	if d, again := slot.Request(j80); d != nil || again != nil {
		t.Fatal("a pending key should not start a second render")
	}
	_, run120 := slot.Request(j120) // resize before the first render finishes
	r120 := run120()
	if !slot.Accept(r120) || r120.Doc == nil || r120.Err != nil {
		t.Fatalf("current result rejected: %+v", r120)
	}
	r80 := run80() // the old width arrives late
	if slot.Accept(r80) {
		t.Error("stale result accepted for display")
	}
	if got, hit := slot.Request(j80); !hit2(got, hit) { // back to 80: a cache hit, no new render
		t.Error("stale result was not kept in the cache")
	}
	if _, run := slot.Request(Job{RecordID: "r", Revision: 2, Content: body, Width: 80, Style: Dark}); run == nil {
		t.Error("a new revision did not miss the cache")
	}
}

func hit2(d *Doc, run func() Result) bool { return d != nil && run == nil }

func TestServiceJoinsConcurrentRequestsForOneKey(t *testing.T) {
	svc := NewService(DefaultCacheBytes)
	j := Job{RecordID: "r", Revision: 1, Content: fixtures()[4].Body, Width: 90, Style: Light}
	var wg sync.WaitGroup
	docs := make([]*Doc, 8)
	for i := range docs {
		wg.Add(1)
		go func() { defer wg.Done(); docs[i] = svc.Run(j).Doc }()
	}
	wg.Wait()
	for i, d := range docs {
		if d == nil || d != docs[0] {
			t.Fatalf("result %d differs: one render should serve every concurrent request", i)
		}
	}
	if d, ok := svc.Lookup(j); !ok || d != docs[0] {
		t.Error("result not cached")
	}
}

func TestConcurrentRendersGiveIdenticalOutput(t *testing.T) {
	fx := fixtures()
	want := make([]string, len(fx))
	job := func(i int) Job {
		return Job{RecordID: fx[i].Title, Revision: fx[i].Rev, Content: fx[i].Body, Width: 70 + i, Style: styles[i%3]}
	}
	for i := range fx {
		want[i] = joined(mustRender(t, job(i)))
	}
	var wg sync.WaitGroup
	for g := range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			i := g % len(fx)
			d, err := Render(job(i))
			if err != nil || joined(d) != want[i] {
				t.Errorf("goroutine %d: output differs (err=%v)", g, err)
			}
		}()
	}
	wg.Wait()
}
