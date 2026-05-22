# Memory Pool (`internal/pool`)

A generic memory pool with automatic growth, built on `sync.Pool` for thread-safe concurrent access.

## Features

- **Pre-allocation**: Eagerly allocates a configurable number of objects at creation time
- **Auto-growth**: Transparently allocates new objects when all pooled items are in use
- **Thread-safe**: Built on `sync.Pool`; safe for concurrent use without external locking
- **Sanitization**: Optional reset function called on every `Put` before recycling
- **Observability**: Atomic statistics track gets, puts, and allocations
- **Generic**: Works with any type via Go generics
- **Byte-slice specialization**: `BytesPool` provides convenient fixed-capacity byte buffers

## Usage

### BytesPool (most common)

```go
import "github.com/phone-talk/agentd/internal/pool"

// 4 KiB buffers, 64 pre-allocated
p := pool.NewBytesPool(4096, 64)

buf := p.Get()
defer p.Put(buf)

buf = append(buf, []byte("data")...)
// use buf...
```

### Generic Pool

```go
type Connection struct {
    id   int
    conn net.Conn
}

p := pool.New(
    func() Connection { return Connection{} },
    func(c Connection) {
        if c.conn != nil {
            c.conn.Close()
        }
        c.id = 0
    },
    32, // pre-allocate 32 connections
)

c := p.Get()
defer p.Put(c)
```

## Performance

Benchmarks compare pooled allocation against raw `make` (4 KiB buffers):

```
BenchmarkBytesPool-10            888367   1355 ns/op     24 B/op   1 allocs/op
BenchmarkBytesPoolParallel-10   5715669    209 ns/op     24 B/op   1 allocs/op
BenchmarkAllocNoPool-10         1957159    513 ns/op   4096 B/op   1 allocs/op
```

- **Zero per-op allocation** of the 4 KiB backing array (saves **~4 KB/op**)
- **2.5× faster** under parallel contention (avoiding allocator lock)
- Single-threaded latency is higher due to zeroing overhead; tune `size` to match your workload

The 24 B/op in the pool benchmark is from the `append` result escaping to the heap; the 4 KiB backing array itself is pooled and reused.

## Design Notes

### Auto-Growth

`sync.Pool` handles growth transparently: when the pool is empty, `Get` invokes the `New` function. The pool tracks how often this happens via `Stats.Allocs`.

### GC Interaction

`sync.Pool` is cleared at each GC cycle. Objects not in active use are collected, preventing unbounded memory growth. The pre-allocated seed helps survive the first GC.

### Reset Function

The reset function is the right place to:
- Clear sensitive data (crypto keys, passwords)
- Close file descriptors or network connections
- Reset length/capacity fields

It is NOT called on the initial `New` allocation, only on `Put`.

### When NOT to Use

- Objects with widely varying sizes (use `make` directly)
- Very large objects where retention causes memory bloat
- Objects that must outlive the goroutine that obtained them
