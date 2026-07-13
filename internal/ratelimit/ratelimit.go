package ratelimit

import (
	"sync"
	"time"
)

// Limiter is a simple in-memory sliding window rate limiter.
// In a multi-instance deployment swap this for Redis, but for a single
// Go binary it works fine.
type Limiter struct {
	mu     sync.Mutex
	hits   map[string][]int64 // bucket -> unix-ms timestamps
	Limit  int
	Window time.Duration
}

func New(limit int, window time.Duration) *Limiter {
	return &Limiter{hits: make(map[string][]int64), Limit: limit, Window: window}
}

type Result struct {
	Allowed   bool
	Remaining int
	ResetAt   int64 // unix-ms
}

func (l *Limiter) Allow(key string) Result {
	now := time.Now().UnixMilli()
	cutoff := now - l.Window.Milliseconds()

	l.mu.Lock()
	defer l.mu.Unlock()

	bucket := l.hits[key]
	// drop expired
	keep := bucket[:0]
	for _, t := range bucket {
		if t > cutoff {
			keep = append(keep, t)
		}
	}
	bucket = keep
	if len(bucket) >= l.Limit {
		resetAt := bucket[0] + l.Window.Milliseconds()
		l.hits[key] = bucket
		return Result{Allowed: false, Remaining: 0, ResetAt: resetAt}
	}
	bucket = append(bucket, now)
	l.hits[key] = bucket
	return Result{Allowed: true, Remaining: l.Limit - len(bucket), ResetAt: now + l.Window.Milliseconds()}
}

// RetryAfterSeconds is a small helper for the X-Retry-After header.
func RetryAfterSeconds(resetMs int64) int {
	d := resetMs - time.Now().UnixMilli()
	if d < 0 {
		return 0
	}
	return int((d + 999) / 1000)
}
