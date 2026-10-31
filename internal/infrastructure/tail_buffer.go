package infrastructure

import "sync"

// TailBuffer is a bounded writer that keeps at most limit bytes from the end of a stream.
type TailBuffer struct {
	mu    sync.Mutex
	buf   []byte
	limit int
}

// NewTailBuffer creates a TailBuffer with a fixed byte limit.
func NewTailBuffer(limit int) *TailBuffer {
	if limit <= 0 {
		limit = 1024 * 1024 // 1 MiB default
	}
	capSize := limit
	if capSize > 4096 {
		capSize = 4096
	}
	return &TailBuffer{
		limit: limit,
		buf:   make([]byte, 0, capSize),
	}
}

// Write appends bytes, discarding older bytes if limit is exceeded.
func (b *TailBuffer) Write(p []byte) (n int, err error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	n = len(p)
	if len(p) >= b.limit {
		b.buf = append(b.buf[:0], p[len(p)-b.limit:]...)
		return n, nil
	}

	spaceNeeded := len(b.buf) + len(p) - b.limit
	if spaceNeeded > 0 {
		copy(b.buf, b.buf[spaceNeeded:])
		b.buf = b.buf[:len(b.buf)-spaceNeeded]
	}
	b.buf = append(b.buf, p...)
	return n, nil
}

// Bytes returns a copy of the tail buffer bytes.
func (b *TailBuffer) Bytes() []byte {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]byte, len(b.buf))
	copy(out, b.buf)
	return out
}
