package ratelimit

import (
	"encoding/json"
	"log"
	"math"
	"net"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/iboondk/tenantly/internal/auth"
)

// Middleware rate-limits each request by keyFn. On a Redis error it fails
// open: the request is served and the error is logged.
func Middleware(l *Limiter, keyFn func(*http.Request) string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			res, err := l.Allow(r.Context(), keyFn(r))
			if err != nil {
				log.Printf("ratelimit: failing open: %v", err)
				next.ServeHTTP(w, r)
				return
			}
			w.Header().Set("X-RateLimit-Limit", strconv.Itoa(l.limit))
			w.Header().Set("X-RateLimit-Remaining", strconv.Itoa(res.Remaining))
			if !res.Allowed {
				secs := int(math.Ceil(res.RetryAfter.Seconds()))
				if secs < 1 {
					secs = 1
				}
				w.Header().Set("Retry-After", strconv.Itoa(secs))
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusTooManyRequests)
				_ = json.NewEncoder(w).Encode(map[string]string{
					"error":   "rate_limited",
					"message": "rate limit exceeded, retry later",
				})
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// KeyByTenantUser keys by the authenticated user, and by tenant when the
// route has a tenant ID. Without claims it keys by the client IP.
//
// The tenant is read from the chi URL parameter. Middleware attached to a
// route group runs before the tenant subrouter matches, so the parameter may
// not be set yet. In that case the tenant is read from the path
// /v1/tenants/{tenantID}/.
func KeyByTenantUser(r *http.Request) string {
	claims, ok := auth.FromContext(r.Context())
	if !ok {
		return "anon:" + remoteIP(r)
	}
	key := "u:" + claims.UserID.String()
	if tenant := tenantFromRequest(r); tenant != "" {
		key += ":t:" + tenant
	}
	return key
}

func tenantFromRequest(r *http.Request) string {
	if t := chi.URLParam(r, "tenantID"); t != "" {
		return t
	}
	rest, ok := strings.CutPrefix(r.URL.Path, "/v1/tenants/")
	if !ok {
		return ""
	}
	seg, _, _ := strings.Cut(rest, "/")
	return seg
}

func remoteIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
