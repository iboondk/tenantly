package ratelimit

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/iboondk/tenantly/internal/auth"
)

func okHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
}

func fixedKey(*http.Request) string { return "u:test" }

func TestMiddlewareDeniesWith429AndRetryAfter(t *testing.T) {
	_, rdb := newTestRedis(t)
	clk := &fakeClock{t: start}
	l := New(rdb, 2, time.Minute, clk.Now)
	h := Middleware(l, fixedKey)(okHandler())

	for i := 0; i < 2; i++ {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("request %d status = %d, want 200", i+1, rec.Code)
		}
	}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429", rec.Code)
	}

	retry := rec.Header().Get("Retry-After")
	secs, err := strconv.Atoi(retry)
	if err != nil || secs < 1 || secs > 60 {
		t.Fatalf("Retry-After = %q, want integer seconds in [1,60]", retry)
	}

	if got := rec.Header().Get("X-RateLimit-Limit"); got != "2" {
		t.Fatalf("X-RateLimit-Limit = %q, want 2", got)
	}
	if got := rec.Header().Get("X-RateLimit-Remaining"); got != "0" {
		t.Fatalf("X-RateLimit-Remaining = %q, want 0", got)
	}

	var body struct {
		Error   string `json:"error"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("body is not JSON: %s", rec.Body)
	}
	if body.Error != "rate_limited" || body.Message == "" {
		t.Fatalf("body = %+v, want error rate_limited with a message", body)
	}
}

func TestMiddlewareRetryAfterIsAtLeastOneSecond(t *testing.T) {
	_, rdb := newTestRedis(t)
	// Near the end of the bucket the remaining time is well under a second.
	clk := &fakeClock{t: start.Add(time.Minute - 100*time.Millisecond)}
	l := New(rdb, 1, time.Minute, clk.Now)
	h := Middleware(l, fixedKey)(okHandler())

	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429", rec.Code)
	}
	if got := rec.Header().Get("Retry-After"); got != "1" {
		t.Fatalf("Retry-After = %q, want 1 (ceil, min 1)", got)
	}
}

func TestMiddlewareFailsOpenWhenRedisIsDown(t *testing.T) {
	mr, rdb := newTestRedis(t)
	mr.Close()
	l := New(rdb, 1, time.Minute, nil)

	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusTeapot)
	})
	rec := httptest.NewRecorder()
	Middleware(l, fixedKey)(next).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if !called {
		t.Fatal("next handler not called; limiter must fail open")
	}
	if rec.Code != http.StatusTeapot {
		t.Fatalf("status = %d, want the handler's 418", rec.Code)
	}
}

func TestKeyByTenantUserAnonymousUsesIP(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "203.0.113.7:4321"
	if got := KeyByTenantUser(req); got != "anon:203.0.113.7" {
		t.Fatalf("key = %q, want anon:203.0.113.7", got)
	}
}

func TestKeyByTenantUserAuthenticated(t *testing.T) {
	uid := uuid.MustParse("11111111-2222-3333-4444-555555555555")
	tid := "66666666-7777-8888-9999-000000000000"

	// withChiTenant puts a chi RouteContext with tenantID set, as the
	// tenant subrouter does once it has matched.
	withChiTenant := func(ctx context.Context) context.Context {
		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("tenantID", tid)
		return context.WithValue(ctx, chi.RouteCtxKey, rctx)
	}

	tests := []struct {
		name    string
		path    string
		chiTid  bool
		wantKey string
	}{
		{
			name:    "no tenant in route or path",
			path:    "/v1/me",
			wantKey: "u:" + uid.String(),
		},
		{
			name:    "tenant from chi URL param",
			path:    "/v1/tenants/" + tid + "/projects",
			chiTid:  true,
			wantKey: "u:" + uid.String() + ":t:" + tid,
		},
		{
			name:    "tenant from path when chi param is not set yet",
			path:    "/v1/tenants/" + tid + "/projects",
			wantKey: "u:" + uid.String() + ":t:" + tid,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tc.path, nil)
			ctx := auth.WithClaims(req.Context(), auth.Claims{UserID: uid, Email: "u@example.com"})
			if tc.chiTid {
				ctx = withChiTenant(ctx)
			}
			req = req.WithContext(ctx)

			if got := KeyByTenantUser(req); got != tc.wantKey {
				t.Fatalf("key = %q, want %q", got, tc.wantKey)
			}
		})
	}
}
