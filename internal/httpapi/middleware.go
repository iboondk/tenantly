package httpapi

import (
	"context"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/iboondk/tenantly/internal/auth"
	"github.com/iboondk/tenantly/internal/authz"
	"github.com/iboondk/tenantly/internal/store"
)

type membershipKey struct{}

// Authenticate requires a valid Bearer token and stores its claims in the
// request context. Anything else gets 401.
func Authenticate(iss *auth.Issuer) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			h := r.Header.Get("Authorization")
			raw, ok := strings.CutPrefix(h, "Bearer ")
			if !ok || raw == "" {
				writeError(w, http.StatusUnauthorized, "unauthorized", "missing bearer token")
				return
			}
			claims, err := iss.Verify(strings.TrimSpace(raw))
			if err != nil {
				writeError(w, http.StatusUnauthorized, "unauthorized", "invalid token")
				return
			}
			next.ServeHTTP(w, r.WithContext(auth.WithClaims(r.Context(), claims)))
		})
	}
}

// RequireMembership reads the {tenantID} URL parameter, checks the caller's
// membership and role through the Authorizer, and stores the membership in the
// context. A malformed tenant ID gets 404, the same as a non-member.
func RequireMembership(a *authz.Authorizer, need string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			tenantID, err := uuid.Parse(chi.URLParam(r, "tenantID"))
			if err != nil {
				writeError(w, http.StatusNotFound, "not_found", "not found")
				return
			}
			m, err := a.Check(r.Context(), tenantID, need)
			if err != nil {
				writeErr(w, err)
				return
			}
			next.ServeHTTP(w, r.WithContext(withMembership(r.Context(), m)))
		})
	}
}

func withMembership(ctx context.Context, m store.Membership) context.Context {
	return context.WithValue(ctx, membershipKey{}, m)
}

// membershipFromContext returns the membership set by RequireMembership.
func membershipFromContext(ctx context.Context) (store.Membership, bool) {
	m, ok := ctx.Value(membershipKey{}).(store.Membership)
	return m, ok
}
