package eventbuf

import "sync"

// Event is a single buffered event with a monotonic sequence number.
type Event struct {
	Seq  uint64         `json:"seq"`
	Data map[string]any `json:"data"`
}

// EventBuffer is a capped circular buffer of Events, safe for concurrent use.
// Uses head/count indices for O(1) append instead of O(n) shift.
type EventBuffer struct {
	mu    sync.Mutex
	cap   int
	seq   uint64
	buf   []Event
	head  int // index of oldest element
	count int // number of elements currently in buffer
}

// New creates an EventBuffer with the given capacity.
func New(cap int) *EventBuffer {
	return &EventBuffer{cap: cap, buf: make([]Event, cap)}
}

// Append adds data to the buffer, evicting the oldest entry if at capacity. O(1).
func (eb *EventBuffer) Append(data map[string]any) uint64 {
	eb.mu.Lock()
	defer eb.mu.Unlock()
	eb.seq++
	e := Event{Seq: eb.seq, Data: data}
	if eb.count < eb.cap {
		// Buffer not yet full: write at head+count
		eb.buf[(eb.head+eb.count)%eb.cap] = e
		eb.count++
	} else {
		// Buffer full: overwrite oldest at head, advance head
		eb.buf[eb.head] = e
		eb.head = (eb.head + 1) % eb.cap
	}
	return eb.seq
}

// Since returns all events with Seq > afterSeq, in order.
func (eb *EventBuffer) Since(afterSeq uint64) []Event {
	eb.mu.Lock()
	defer eb.mu.Unlock()
	var result []Event
	for i := 0; i < eb.count; i++ {
		e := eb.buf[(eb.head+i)%eb.cap]
		if e.Seq > afterSeq {
			result = append(result, e)
		}
	}
	return result
}

// LastSeq returns the highest sequence number appended so far.
func (eb *EventBuffer) LastSeq() uint64 {
	eb.mu.Lock()
	defer eb.mu.Unlock()
	return eb.seq
}
