package ratelimit

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

// fakeClock is a settable clock for the limiter's injected now func.
type fakeClock struct{ t time.Time }

func (c *fakeClock) Now() time.Time          { return c.t }
func (c *fakeClock) Advance(d time.Duration) { c.t = c.t.Add(d) }

func newTestRedis(t *testing.T) (*miniredis.Miniredis, *redis.Client) {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr(), MaxRetries: -1})
	t.Cleanup(func() { _ = rdb.Close() })
	return mr, rdb
}

// Start at a bucket boundary so the whole window fits in one bucket.
var start = time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)

func TestLimiterFixedWindow(t *testing.T) {
	mr, rdb := newTestRedis(t)
	clk := &fakeClock{t: start}
	l := New(rdb, 3, time.Minute, clk.Now)
	ctx := context.Background()

	for i := 1; i <= 3; i++ {
		res, err := l.Allow(ctx, "u:alice")
		if err != nil {
			t.Fatalf("allow %d: %v", i, err)
		}
		if !res.Allowed {
			t.Fatalf("request %d denied, want allowed", i)
		}
		if want := 3 - i; res.Remaining != want {
			t.Fatalf("request %d remaining = %d, want %d", i, res.Remaining, want)
		}
	}

	res, err := l.Allow(ctx, "u:alice")
	if err != nil {
		t.Fatalf("4th allow: %v", err)
	}
	if res.Allowed {
		t.Fatal("4th request allowed, want denied")
	}
	if res.RetryAfter <= 0 || res.RetryAfter > time.Minute {
		t.Fatalf("RetryAfter = %v, want within (0, 1m]", res.RetryAfter)
	}
	if res.Remaining != 0 {
		t.Fatalf("Remaining = %d on deny, want 0", res.Remaining)
	}

	// The bucket key follows the spec format.
	bucket := start.UnixMilli() / time.Minute.Milliseconds()
	if !mr.Exists("tenantly:rl:u:alice:" + strconv.FormatInt(bucket, 10)) {
		t.Fatal("expected redis key tenantly:rl:u:alice:<bucket>")
	}

	// Advance past the window: the next bucket starts with a fresh count.
	clk.Advance(time.Minute)
	mr.FastForward(time.Minute)
	res, err = l.Allow(ctx, "u:alice")
	if err != nil {
		t.Fatalf("after window: %v", err)
	}
	if !res.Allowed {
		t.Fatal("request after window advance denied, want allowed")
	}
}

func TestLimiterKeysAreIndependent(t *testing.T) {
	_, rdb := newTestRedis(t)
	clk := &fakeClock{t: start}
	l := New(rdb, 1, time.Minute, clk.Now)
	ctx := context.Background()

	if res, _ := l.Allow(ctx, "u:alice"); !res.Allowed {
		t.Fatal("alice first request denied")
	}
	if res, _ := l.Allow(ctx, "u:alice"); res.Allowed {
		t.Fatal("alice second request allowed, want denied")
	}
	if res, _ := l.Allow(ctx, "u:bob"); !res.Allowed {
		t.Fatal("bob denied because of alice's count")
	}
}

func TestLimiterRedisErrorIsReturned(t *testing.T) {
	mr, rdb := newTestRedis(t)
	mr.Close()
	l := New(rdb, 3, time.Minute, nil)
	if _, err := l.Allow(context.Background(), "u:alice"); err == nil {
		t.Fatal("expected error when redis is closed")
	}
}
