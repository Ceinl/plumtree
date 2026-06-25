package sshgateway

import "testing"

func TestCapWriterTruncates(t *testing.T) {
	w := newCapWriter(10)

	if n, err := w.Write([]byte("hello")); err != nil || n != 5 {
		t.Fatalf("Write(hello) = %d,%v want 5,nil", n, err)
	}
	if w.truncated {
		t.Fatal("not truncated after 5/10 bytes")
	}

	// "world!!" overflows: only the first 5 bytes fit, the rest is dropped, but
	// Write still reports the full length so the guest never sees a short write.
	if n, err := w.Write([]byte("world!!")); err != nil || n != 7 {
		t.Fatalf("Write(world!!) = %d,%v want 7,nil", n, err)
	}
	if !w.truncated {
		t.Fatal("expected truncated after overflow")
	}
	if got := w.String(); got != "helloworld" {
		t.Fatalf("buffer = %q, want %q", got, "helloworld")
	}

	// Further writes do not grow the buffer past the cap.
	w.Write([]byte("more"))
	if got := w.String(); len(got) != 10 {
		t.Fatalf("buffer grew past cap: %q", got)
	}
}
