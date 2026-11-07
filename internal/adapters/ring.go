package adapters

import (
	"sync"

	"survy/internal/ports"
)

// destroyedBuffer is a bounded FIFO of destroyed flows. When full, the oldest
// flow is dropped and counted so memory stays bounded if nobody drains.
type destroyedBuffer struct {
	mu    sync.Mutex
	buf   []ports.Flow
	head  int // index of oldest element
	n     int // number of elements
	drops uint64
}

func newDestroyedBuffer(capacity int) *destroyedBuffer {
	if capacity < 1 {
		capacity = 1
	}
	return &destroyedBuffer{buf: make([]ports.Flow, capacity)}
}

func (b *destroyedBuffer) push(f ports.Flow) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.n == len(b.buf) {
		b.buf[b.head] = f
		b.head = (b.head + 1) % len(b.buf)
		b.drops++
		return
	}
	b.buf[(b.head+b.n)%len(b.buf)] = f
	b.n++
}

func (b *destroyedBuffer) drain() []ports.Flow {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]ports.Flow, b.n)
	for i := range out {
		out[i] = b.buf[(b.head+i)%len(b.buf)]
	}
	b.head, b.n = 0, 0
	return out
}

func (b *destroyedBuffer) dropped() uint64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.drops
}
