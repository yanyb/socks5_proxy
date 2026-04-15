package client

import (
	"math"
	"math/rand/v2"
	"time"
)

// reconnectBackoff implements exponential backoff with jitter after failed connects or dropped sessions.
// Call Reset after a successful registration so the next disconnect starts from the initial delay again.
type reconnectBackoff struct {
	initial, max time.Duration
	failures     int
}

func newReconnectBackoff(initial, max time.Duration) *reconnectBackoff {
	if initial <= 0 {
		initial = time.Second
	}
	if max <= 0 || max < initial {
		max = 60 * time.Second
	}
	return &reconnectBackoff{initial: initial, max: max}
}

func (b *reconnectBackoff) Reset() {
	b.failures = 0
}

// Next returns sleep duration before the next connection attempt (after a failure).
func (b *reconnectBackoff) Next() time.Duration {
	// delay = min(max, initial * 2^failures), then 50%–100% jitter
	exp := float64(b.initial) * math.Pow(2, float64(b.failures))
	b.failures++
	if exp > float64(b.max) {
		exp = float64(b.max)
	}
	base := time.Duration(exp)
	jit := 0.5 + rand.Float64()*0.5
	d := time.Duration(float64(base) * jit)
	if d > b.max {
		d = b.max
	}
	if d < time.Millisecond {
		d = time.Millisecond
	}
	return d
}
