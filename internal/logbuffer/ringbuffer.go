package logbuffer

import (
	"sync"
)

type RingBuffer struct {
	mu    sync.Mutex
	lines []string
	cap   int
	head  int
	size  int
	// written counts every line ever written and never decreases, not even on
	// Clear. It gives each line a stable global index so readers (e.g. the MCP
	// get_logs cursor) can ask for "everything after index N" without re-reading
	// lines they already consumed.
	written int
}

func New(capacity int) *RingBuffer {
	if capacity < 1 {
		capacity = 500
	}
	return &RingBuffer{
		lines: make([]string, capacity),
		cap:   capacity,
	}
}

func (rb *RingBuffer) Write(line string) {
	rb.mu.Lock()
	defer rb.mu.Unlock()
	rb.lines[rb.head] = line
	rb.head = (rb.head + 1) % rb.cap
	rb.written++
	if rb.size < rb.cap {
		rb.size++
	}
}

// Written returns the total number of lines ever written, which is also the
// global index the next written line will take.
func (rb *RingBuffer) Written() int {
	rb.mu.Lock()
	defer rb.mu.Unlock()
	return rb.written
}

// LinesSince returns the retained lines whose global index is >= since, along
// with the index to pass on the next call. A since below the oldest retained
// index is clamped, so a slow reader silently skips the lines that were already
// evicted instead of getting an error.
func (rb *RingBuffer) LinesSince(since int) (lines []string, next int) {
	all := rb.Lines()

	rb.mu.Lock()
	written, size := rb.written, rb.size
	rb.mu.Unlock()

	oldest := written - size
	if since < oldest {
		since = oldest
	}
	if since >= written {
		return nil, written
	}
	return all[since-oldest:], written
}

func (rb *RingBuffer) Lines() []string {
	rb.mu.Lock()
	defer rb.mu.Unlock()
	result := make([]string, 0, rb.size)
	if rb.size < rb.cap {
		result = append(result, rb.lines[:rb.size]...)
	} else {
		start := rb.head
		result = append(result, rb.lines[start:]...)
		result = append(result, rb.lines[:start]...)
	}
	return result
}

func (rb *RingBuffer) Clear() {
	rb.mu.Lock()
	defer rb.mu.Unlock()
	rb.head = 0
	rb.size = 0
}
