package runner

import (
	"testing"
	"time"
)

func TestTokenBucketAllowAndRefill(t *testing.T) {
	now := time.Unix(0, 0)
	b := newTokenBucketClock(10, func() time.Time { return now })

	for i := 0; i < 10; i++ {
		if !b.allow() {
			t.Fatalf("token %d within burst should be allowed", i)
		}
	}
	if b.allow() {
		t.Fatal("11th token should be denied (burst exhausted)")
	}

	now = now.Add(500 * time.Millisecond) // refills 0.5s * 10/s = 5 tokens
	got := 0
	for b.allow() {
		got++
	}
	if got != 5 {
		t.Fatalf("after 0.5s got %d tokens, want 5", got)
	}
}

func TestTokenBucketUnlimited(t *testing.T) {
	b := newTokenBucket(0)
	for i := 0; i < 1000; i++ {
		if !b.allow() {
			t.Fatal("unlimited bucket must always allow")
		}
	}
	if !b.wait(nil) {
		t.Fatal("unlimited wait must return true immediately")
	}
}

func TestTokenBucketWaitCancelled(t *testing.T) {
	b := newTokenBucket(1) // burst 1
	if !b.allow() {
		t.Fatal("first token should be allowed")
	}
	done := make(chan struct{})
	close(done)
	if b.wait(done) {
		t.Fatal("wait should return false when done is already closed")
	}
}
