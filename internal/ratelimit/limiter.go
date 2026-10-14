// Package ratelimit implements a fixed-window rate limiter backed by Redis.
//
// Each key gets one counter per window bucket. The counter is incremented
// and given a TTL in one Lua script, so the update is atomic across API
// replicas.
package ratelimit

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

const keyPrefix = "tenantly:rl:"

// incrScript increments the bucket counter. The TTL is set only on the first
// increment, so the bucket expires one window after it was opened.
var incrScript = redis.NewScript(`
local c = redis.call('INCR', KEYS[1])
if c == 1 then
  redis.call('PEXPIRE', KEYS[1], ARGV[1])
end
return c
`)

// Result is the outcome of one Allow call.
type Result struct {
	Allowed    bool
	Remaining  int
	RetryAfter time.Duration
}

// Limiter allows at most limit requests per key in each window.
type Limiter struct {
	rdb    redis.Scripter
	limit  int
	window time.Duration
	now    func() time.Time
}

// New returns a Limiter. A nil now uses time.Now. A window shorter than one
// millisecond is raised to one millisecond.
func New(rdb redis.Scripter, limit int, window time.Duration, now func() time.Time) *Limiter {
	if now == nil {
		now = time.Now
	}
	if window < time.Millisecond {
		window = time.Millisecond
	}
	return &Limiter{rdb: rdb, limit: limit, window: window, now: now}
}

// Allow counts one request for key and reports whether it is within the limit.
// RetryAfter is the time until the current bucket ends. A Redis error is
// returned as is; the caller decides whether to fail open.
func (l *Limiter) Allow(ctx context.Context, key string) (Result, error) {
	now := l.now()
	windowMs := l.window.Milliseconds()
	bucket := now.UnixMilli() / windowMs
	rkey := fmt.Sprintf("%s%s:%d", keyPrefix, key, bucket)

	count, err := incrScript.Run(ctx, l.rdb, []string{rkey}, windowMs).Int64()
	if err != nil {
		return Result{}, fmt.Errorf("ratelimit: incr: %w", err)
	}

	remaining := int64(l.limit) - count
	if remaining < 0 {
		remaining = 0
	}
	bucketEnd := time.UnixMilli((bucket + 1) * windowMs)
	return Result{
		Allowed:    count <= int64(l.limit),
		Remaining:  int(remaining),
		RetryAfter: bucketEnd.Sub(now),
	}, nil
}
