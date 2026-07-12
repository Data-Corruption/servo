package ops

import "sync"

// ringCap is how much recent driver output we keep in memory. Old output
// scrolls off — nobody needs megabytes of image-pull progress bars.
const ringCap = 64 * 1024

// ring is a byte ring buffer that also tracks the total number of bytes ever
// written, so pollers can ask "give me everything after offset N" and detect
// when they've fallen off the back.
type ring struct {
	mu    sync.Mutex
	data  []byte
	total int64
}

func newRing() *ring {
	return &ring{}
}

// Write implements io.Writer. Safe for concurrent use.
func (b *ring) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.total += int64(len(p))
	b.data = append(b.data, p...)
	if len(b.data) > ringCap {
		b.data = b.data[len(b.data)-ringCap:]
	}
	return len(p), nil
}

// ReadFrom returns all buffered bytes after offset and the new offset to poll
// from. If offset predates the buffered window (client fell behind or a new
// op reset the buffer), it returns from the earliest available byte.
func (b *ring) ReadFrom(offset int64) ([]byte, int64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	earliest := b.total - int64(len(b.data))
	if offset < earliest || offset > b.total {
		offset = earliest
	}
	out := make([]byte, b.total-offset)
	copy(out, b.data[len(b.data)-int(b.total-offset):])
	return out, b.total
}

// Reset clears the buffer for a new operation. The total offset keeps
// growing monotonically so stale client offsets self-correct.
func (b *ring) Reset() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.data = b.data[:0]
}

// Tail returns up to n bytes of the newest buffered output as a string.
func (b *ring) Tail(n int) string {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.data) <= n {
		return string(b.data)
	}
	return string(b.data[len(b.data)-n:])
}
