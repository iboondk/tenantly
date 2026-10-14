package auth

import "context"

type claimsKey struct{}

// WithClaims returns a copy of ctx that carries c.
func WithClaims(ctx context.Context, c Claims) context.Context {
	return context.WithValue(ctx, claimsKey{}, c)
}

// FromContext returns the claims stored by WithClaims.
func FromContext(ctx context.Context) (Claims, bool) {
	c, ok := ctx.Value(claimsKey{}).(Claims)
	return c, ok
}
