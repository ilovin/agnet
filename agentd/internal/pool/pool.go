// Package pool provides memory pools with automatic growth for reducing GC pressure.
//
// The pools pre-allocate blocks on creation and transparently allocate additional
// blocks when demand exceeds the pre-allocated set. Returned blocks are recycled
// via sync.Pool, making the pools safe for concurrent use.
//
// Typical usage:
//
//	p := pool.NewBytesPool(4096, 64) // 4 KiB buffers, 64 pre-allocated
//	buf := p.Get()
//	defer p.Put(buf)
//	// use buf...
package pool

import (
	"sync"
	"sync/atomic"
)

// Stats holds atomic counters for pool observability.
type Stats struct {
	Gets   uint64 // total Get calls
	Puts   uint64 // total Put calls
	Allocs uint64 // times the pool had to allocate a new block
}

// Pool is a generic memory pool of type T.
//
// It wraps sync.Pool to provide:
//   - Pre-allocation on creation
//   - Automatic growth when all pooled objects are in use
//   - Optional reset function to sanitize objects before reuse
//   - Atomic statistics
//
// The zero value is NOT usable; use New to create a pool.
type Pool[T any] struct {
	pool      sync.Pool
	newFunc   func() T
	resetFunc func(T)

	gets   atomic.Uint64
	puts   atomic.Uint64
	allocs atomic.Uint64
}

// New creates a Pool.
//
// newFunc must allocate and return a zero or initialized T.
// resetFunc is called on every Put before the value is returned to the pool;
// it may be nil if no cleanup is required.
// prealloc controls how many objects are eagerly allocated at creation time.
func New[T any](newFunc func() T, resetFunc func(T), prealloc int) *Pool[T] {
	if newFunc == nil {
		panic("pool.New: newFunc is nil")
	}

	p := &Pool[T]{
		newFunc:   newFunc,
		resetFunc: resetFunc,
	}
	p.pool.New = func() any {
		p.allocs.Add(1)
		return newFunc()
	}

	// Pre-allocate and seed the pool.
	for i := 0; i < prealloc; i++ {
		p.pool.Put(newFunc())
	}

	return p
}

// Get obtains an item from the pool.
// If the pool is empty, a new item is allocated transparently (auto-growth).
func (p *Pool[T]) Get() T {
	p.gets.Add(1)
	return p.pool.Get().(T)
}

// Put returns an item to the pool.
// If a resetFunc was provided, it is applied before recycling.
func (p *Pool[T]) Put(x T) {
	p.puts.Add(1)
	if p.resetFunc != nil {
		p.resetFunc(x)
	}
	p.pool.Put(x)
}

// Stats returns a snapshot of the current statistics.
func (p *Pool[T]) Stats() Stats {
	return Stats{
		Gets:   p.gets.Load(),
		Puts:   p.puts.Load(),
		Allocs: p.allocs.Load(),
	}
}

// ResetStats zeroes all statistics counters.
func (p *Pool[T]) ResetStats() {
	p.gets.Store(0)
	p.puts.Store(0)
	p.allocs.Store(0)
}

// BytesPool is a convenience pool for byte slices of a fixed capacity.
//
// Slices returned by Get are guaranteed to have cap == size.
// The length is zeroed on Put, but callers should still treat the
// underlying bytes as uninitialized.
type BytesPool struct {
	pool *Pool[[]byte]
	size int
}

// NewBytesPool creates a BytesPool where each buffer has the given byte size.
// prealloc controls how many buffers are eagerly allocated.
func NewBytesPool(size, prealloc int) *BytesPool {
	if size <= 0 {
		panic("pool.NewBytesPool: size must be > 0")
	}
	if prealloc < 0 {
		prealloc = 0
	}

	bp := &BytesPool{size: size}
	bp.pool = New(
		func() []byte { return make([]byte, 0, size) },
		func(b []byte) {
			// Clear the slice header so len==0 while keeping cap intact.
			// Zero out the first min(len, size) bytes to prevent data leaks.
			for i := range b {
				b[i] = 0
			}
		},
		prealloc,
	)
	return bp
}

// Get obtains a zero-length byte slice with capacity size.
func (bp *BytesPool) Get() []byte {
	return bp.pool.Get()
}

// Put returns a byte slice to the pool.
// The slice is zeroed and its length is reset to zero before recycling.
func (bp *BytesPool) Put(b []byte) {
	for i := 0; i < cap(b); i++ {
		b = b[:i+1]
		b[i] = 0
	}
	bp.pool.Put(b[:0])
}

// Stats returns statistics for the underlying pool.
func (bp *BytesPool) Stats() Stats {
	return bp.pool.Stats()
}

// ResetStats zeroes all statistics counters.
func (bp *BytesPool) ResetStats() {
	bp.pool.ResetStats()
}
