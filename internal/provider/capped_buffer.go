package provider

import "bytes"

// CappedBuffer drains all writes while retaining at most limit bytes. Vendor
// CLIs are outside the trusted core and must not be able to exhaust the app's
// memory by flooding an identity/check response.
type CappedBuffer struct {
	buffer   bytes.Buffer
	limit    int
	exceeded bool
}

func NewCappedBuffer(limit int) *CappedBuffer {
	return &CappedBuffer{limit: limit}
}

func (b *CappedBuffer) Write(p []byte) (int, error) {
	written := len(p)
	remaining := b.limit - b.buffer.Len()
	if remaining > 0 {
		if len(p) > remaining {
			_, _ = b.buffer.Write(p[:remaining])
		} else {
			_, _ = b.buffer.Write(p)
		}
	}
	if len(p) > remaining {
		b.exceeded = true
	}
	return written, nil
}

func (b *CappedBuffer) Bytes() []byte  { return b.buffer.Bytes() }
func (b *CappedBuffer) String() string { return b.buffer.String() }
func (b *CappedBuffer) Exceeded() bool { return b.exceeded }
