package slack

import (
	"sync"
	"time"
)

const (
	maxMessagesPerMinute = 5
	bucketWindow         = time.Minute
)

// tokenBucket tracks message counts for a single employee within a rolling window.
type tokenBucket struct {
	mu        sync.Mutex
	count     int
	windowEnd time.Time
}

func (b *tokenBucket) allow() bool {
	b.mu.Lock()
	defer b.mu.Unlock()

	now := time.Now()
	if now.After(b.windowEnd) {
		// New window: reset counter.
		b.count = 0
		b.windowEnd = now.Add(bucketWindow)
	}

	if b.count >= maxMessagesPerMinute {
		return false
	}
	b.count++
	return true
}

// RateLimiter enforces a per-employee outbound message rate limit.
// Max 5 messages per minute per AE (SLACK_NOTES §12).
type RateLimiter struct {
	mu      sync.Mutex
	buckets map[string]*tokenBucket
}

// NewRateLimiter returns an initialised RateLimiter.
func NewRateLimiter() *RateLimiter {
	return &RateLimiter{
		buckets: make(map[string]*tokenBucket),
	}
}

// Allow returns true if the given employee is permitted to send a message now.
func (rl *RateLimiter) Allow(employeeID string) bool {
	rl.mu.Lock()
	b, ok := rl.buckets[employeeID]
	if !ok {
		b = &tokenBucket{}
		rl.buckets[employeeID] = b
	}
	rl.mu.Unlock()

	return b.allow()
}
