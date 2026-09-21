// Package backoff provides the bounded exponential delay with jitter shared by
// server retry loops so repeated failures do not re-synchronize across
// processes.
package backoff

import (
	"math/rand/v2"
	"time"
)

// Delay returns an exponentially growing wait for the zero-based attempt count,
// capped at max, plus up to half the current delay as random jitter.
func Delay(attempt int, base, max time.Duration) time.Duration {
	if base <= 0 {
		base = time.Millisecond
	}
	delay := base
	for range attempt {
		delay *= 2
		if delay >= max {
			break
		}
	}
	if delay <= 0 || delay > max {
		delay = max
	}
	if jitter := delay / 2; jitter > 0 {
		delay += time.Duration(rand.Int64N(int64(jitter)))
	}
	return delay
}
