package provider

import "testing"

func TestCappedBufferDrainsButBoundsStoredBytes(t *testing.T) {
	buffer := NewCappedBuffer(5)
	if n, err := buffer.Write([]byte("abcdefgh")); err != nil || n != 8 {
		t.Fatalf("Write() = (%d, %v), want (8, nil)", n, err)
	}
	if got := string(buffer.Bytes()); got != "abcde" {
		t.Fatalf("stored bytes = %q, want %q", got, "abcde")
	}
	if !buffer.Exceeded() {
		t.Fatal("Exceeded() = false, want true")
	}
	if n, err := buffer.Write([]byte("more")); err != nil || n != 4 {
		t.Fatalf("second Write() = (%d, %v), want (4, nil)", n, err)
	}
	if got := len(buffer.Bytes()); got != 5 {
		t.Fatalf("stored size = %d, want 5", got)
	}
}
