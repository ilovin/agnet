package pool_test

import (
	"sync"
	"testing"

	"github.com/phone-talk/agentd/internal/pool"
)

func TestBytesPoolBasicGetPut(t *testing.T) {
	p := pool.NewBytesPool(1024, 4)

	b1 := p.Get()
	if cap(b1) != 1024 {
		t.Fatalf("expected cap 1024, got %d", cap(b1))
	}
	if len(b1) != 0 {
		t.Fatalf("expected len 0, got %d", len(b1))
	}

	// Write some data
	b1 = append(b1, []byte("hello")...)
	p.Put(b1)

	// Get again — should be zeroed and len==0
	b2 := p.Get()
	if cap(b2) != 1024 {
		t.Fatalf("expected cap 1024 on reuse, got %d", cap(b2))
	}
	if len(b2) != 0 {
		t.Fatalf("expected len 0 after Put, got %d", len(b2))
	}
	// Verify zeroing
	for i := range b2 {
		if b2[i] != 0 {
			t.Fatalf("expected zeroed buffer at index %d, got %d", i, b2[i])
		}
	}
}

func TestBytesPoolPrealloc(t *testing.T) {
	p := pool.NewBytesPool(256, 10)
	p.ResetStats()

	// All 10 gets should come from pre-allocated pool without new allocations.
	bufs := make([][]byte, 10)
	for i := range bufs {
		bufs[i] = p.Get()
	}

	stats := p.Stats()
	if stats.Gets != 10 {
		t.Fatalf("expected 10 gets, got %d", stats.Gets)
	}
	if stats.Allocs > stats.Gets {
		t.Fatalf("expected allocs <= gets, got allocs=%d gets=%d", stats.Allocs, stats.Gets)
	}

	// Return them
	for _, b := range bufs {
		p.Put(b)
	}

	// Re-get — should recycle without heavy allocation
	for i := range bufs {
		bufs[i] = p.Get()
	}
	stats = p.Stats()
	if stats.Allocs > stats.Gets {
		t.Fatalf("expected allocs <= gets after recycling, got allocs=%d gets=%d", stats.Allocs, stats.Gets)
	}
}

func TestBytesPoolAutoGrowth(t *testing.T) {
	p := pool.NewBytesPool(128, 2)
	p.ResetStats()

	// Exhaust pre-allocated pool
	_ = p.Get()
	_ = p.Get()

	// Third Get forces allocation (auto-growth)
	_ = p.Get()

	stats := p.Stats()
	if stats.Allocs != 1 {
		t.Fatalf("expected 1 alloc for auto-growth, got %d", stats.Allocs)
	}
	if stats.Gets != 3 {
		t.Fatalf("expected 3 gets, got %d", stats.Gets)
	}
}

func TestBytesPoolConcurrent(t *testing.T) {
	p := pool.NewBytesPool(512, 64)
	p.ResetStats()

	const workers = 100
	const iterations = 1000

	var wg sync.WaitGroup
	wg.Add(workers)
	for i := 0; i < workers; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				b := p.Get()
				// Simulate some work
				_ = append(b, byte(j))
				p.Put(b)
			}
		}()
	}
	wg.Wait()

	stats := p.Stats()
	if stats.Gets != workers*iterations {
		t.Fatalf("expected %d gets, got %d", workers*iterations, stats.Gets)
	}
	if stats.Puts != workers*iterations {
		t.Fatalf("expected %d puts, got %d", workers*iterations, stats.Puts)
	}
	// Some allocations are expected due to burst demand, but should be
	// far fewer than total gets because of recycling.
	if stats.Allocs >= stats.Gets {
		t.Fatalf("expected fewer allocs than gets, got allocs=%d gets=%d", stats.Allocs, stats.Gets)
	}
}

func TestGenericPoolWithReset(t *testing.T) {
	type item struct {
		value int
		used  bool
	}

	resetCalled := 0
	p := pool.New(
		func() item { return item{} },
		func(i item) {
			resetCalled++
			i.value = 0
			i.used = false
		},
		4,
	)

	it := p.Get()
	it.value = 42
	it.used = true
	p.Put(it)

	if resetCalled != 1 {
		t.Fatalf("expected reset called once, got %d", resetCalled)
	}
}

func TestGenericPoolNilReset(t *testing.T) {
	type simple struct{ v int }

	p := pool.New(
		func() simple { return simple{v: 1} },
		nil,
		2,
	)

	// Should not panic
	s := p.Get()
	p.Put(s)
	if s.v != 1 {
		t.Fatalf("expected v=1, got %d", s.v)
	}
}

func TestPoolStatsReset(t *testing.T) {
	p := pool.NewBytesPool(64, 1)
	p.Get()
	p.Put(make([]byte, 0, 64))

	if p.Stats().Gets == 0 {
		t.Fatal("expected non-zero gets before reset")
	}

	p.ResetStats()
	stats := p.Stats()
	if stats.Gets != 0 || stats.Puts != 0 || stats.Allocs != 0 {
		t.Fatalf("expected all zero after ResetStats, got %+v", stats)
	}
}

func TestBytesPoolPutZeroesData(t *testing.T) {
	p := pool.NewBytesPool(64, 1)

	b := p.Get()
	for i := 0; i < 64; i++ {
		b = append(b, byte(i))
	}
	p.Put(b)

	b2 := p.Get()
	backing := b2[:cap(b2)]
	for i := 0; i < len(backing); i++ {
		if backing[i] != 0 {
			t.Fatalf("expected zero at index %d, got %d", i, backing[i])
		}
	}
}

func TestBytesPoolLargePrealloc(t *testing.T) {
	// Pre-alloc of 0 should still work
	p := pool.NewBytesPool(128, 0)
	p.ResetStats()

	b := p.Get()
	if cap(b) != 128 {
		t.Fatalf("expected cap 128, got %d", cap(b))
	}

	stats := p.Stats()
	if stats.Allocs != 1 {
		t.Fatalf("expected 1 alloc with prealloc=0, got %d", stats.Allocs)
	}
}

func TestPoolPanicOnNilNewFunc(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic for nil newFunc")
		}
	}()
	pool.New[int](nil, nil, 1)
}

func TestBytesPoolPanicOnZeroSize(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic for size <= 0")
		}
	}()
	pool.NewBytesPool(0, 1)
}

var benchResult []byte

func BenchmarkBytesPool(b *testing.B) {
	p := pool.NewBytesPool(4096, 64)
	data := []byte("benchmark")
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		buf := p.Get()
		benchResult = append(buf, data...)
		p.Put(buf)
	}
}

func BenchmarkBytesPoolParallel(b *testing.B) {
	p := pool.NewBytesPool(4096, 64)
	data := []byte("benchmark")
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		var local []byte
		for pb.Next() {
			buf := p.Get()
			local = append(buf, data...)
			p.Put(buf)
		}
		benchResult = local
	})
}

func BenchmarkAllocNoPool(b *testing.B) {
	data := []byte("benchmark")
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		buf := make([]byte, 0, 4096)
		benchResult = append(buf, data...)
	}
}
