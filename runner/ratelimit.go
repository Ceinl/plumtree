package runner

import (
	"sync"
	"time"
)

// tokenBucket is a simple rate limiter: it refills at rate tokens per second up
// to a burst capacity. It is used to bound how fast the host delivers input
// events to the guest and how fast the guest may present frames. The clock is
// injectable for deterministic tests.
type tokenBucket struct {
	mu     sync.Mutex
	rate   float64 // tokens per second; <= 0 disables limiting
	burst  float64
	tokens float64
	last   time.Time
	now    func() time.Time
}

// newTokenBucket returns a bucket that allows perSec events per second with a
// burst of perSec (at least 1). A perSec <= 0 yields a bucket that never limits.
func newTokenBucket(perSec int) *tokenBucket {
	return newTokenBucketClock(perSec, time.Now)
}

func newTokenBucketClock(perSec int, now func() time.Time) *tokenBucket {
	rate := float64(perSec)
	burst := rate
	if burst < 1 {
		burst = 1
	}
	return &tokenBucket{rate: rate, burst: burst, tokens: burst, last: now(), now: now}
}

// allow reports whether one token is available right now, consuming it if so.
// A non-limiting bucket always allows.
func (b *tokenBucket) allow() bool {
	if b.rate <= 0 {
		return true
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.refillLocked()
	if b.tokens >= 1 {
		b.tokens--
		return true
	}
	return false
}

// wait blocks until a token is available or done is closed, returning true once
// a token was consumed and false if done fired first. A non-limiting bucket
// returns immediately.
func (b *tokenBucket) wait(done <-chan struct{}) bool {
	if b.rate <= 0 {
		return true
	}
	for {
		b.mu.Lock()
		b.refillLocked()
		if b.tokens >= 1 {
			b.tokens--
			b.mu.Unlock()
			return true
		}
		// Time until the next whole token.
		deficit := 1 - b.tokens
		delay := time.Duration(deficit / b.rate * float64(time.Second))
		b.mu.Unlock()
		if delay <= 0 {
			delay = time.Millisecond
		}
		t := time.NewTimer(delay)
		select {
		case <-done:
			t.Stop()
			return false
		case <-t.C:
		}
	}
}

func (b *tokenBucket) refillLocked() {
	now := b.now()
	elapsed := now.Sub(b.last).Seconds()
	if elapsed <= 0 {
		return
	}
	b.last = now
	b.tokens += elapsed * b.rate
	if b.tokens > b.burst {
		b.tokens = b.burst
	}
}
