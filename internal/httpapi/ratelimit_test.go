package httpapi

import (
	"net/http"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"

	"github.com/iboondk/tenantly/internal/ratelimit"
)

// TestRateLimitReturns429OnThirdRequest sends authenticated requests with a
// limit of 2 per window. The third must get 429 with a Retry-After header.
func TestRateLimitReturns429OnThirdRequest(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr(), MaxRetries: -1})
	t.Cleanup(func() { _ = rdb.Close() })

	limiter := ratelimit.New(rdb, 2, time.Minute, time.Now)
	a := newTestAPIWith(t, "test", []func(http.Handler) http.Handler{
		ratelimit.Middleware(limiter, ratelimit.KeyByTenantUser),
	})
	token := a.devLogin("ratelimit@example.test")
	path := "/v1/tenants/" + uuid.New().String() + "/projects"

	// Requests 1 and 2 pass the limiter. Membership then gives 404, which
	// is not a rate limit.
	for i := 1; i <= 2; i++ {
		rec := a.do(http.MethodGet, path, token, nil)
		if rec.Code == http.StatusTooManyRequests {
			t.Fatalf("request %d rate limited too early", i)
		}
	}

	rec := a.do(http.MethodGet, path, token, nil)
	requireStatus(t, rec, http.StatusTooManyRequests)
	if rec.Header().Get("Retry-After") == "" {
		t.Fatal("429 without Retry-After")
	}
	requireErrorCode(t, rec, "rate_limited")
}
