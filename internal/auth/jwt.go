// Package auth issues and verifies Tenantly access tokens and runs the OAuth2
// login exchange with a generic provider.
package auth

import (
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

const (
	issuer     = "tenantly"
	signingAlg = "HS256"
)

// Claims identifies the caller. UserID becomes the JWT subject.
type Claims struct {
	UserID uuid.UUID
	Email  string
}

// tokenClaims is the wire format: registered claims plus the email.
type tokenClaims struct {
	Email string `json:"email"`
	jwt.RegisteredClaims
}

// Issuer signs and verifies HS256 tokens. now is injected so tests control time.
type Issuer struct {
	secret []byte
	ttl    time.Duration
	now    func() time.Time
}

// NewIssuer returns an Issuer. A nil now defaults to time.Now.
func NewIssuer(secret []byte, ttl time.Duration, now func() time.Time) *Issuer {
	if now == nil {
		now = time.Now
	}
	return &Issuer{secret: secret, ttl: ttl, now: now}
}

// Issue returns a signed token for c. The subject is the user ID.
func (i *Issuer) Issue(c Claims) (string, error) {
	if c.UserID == uuid.Nil {
		return "", errors.New("auth: issue: user id is required")
	}
	if len(i.secret) == 0 {
		return "", errors.New("auth: issue: secret is empty")
	}
	now := i.now()
	tc := tokenClaims{
		Email: c.Email,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   c.UserID.String(),
			Issuer:    issuer,
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(i.ttl)),
		},
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, tc)
	signed, err := tok.SignedString(i.secret)
	if err != nil {
		return "", fmt.Errorf("auth: sign token: %w", err)
	}
	return signed, nil
}

// Verify checks the signature, the algorithm (HS256 only), the issuer, and the
// expiry, which is required. It returns the claims on success.
func (i *Issuer) Verify(token string) (Claims, error) {
	var tc tokenClaims
	_, err := jwt.ParseWithClaims(token, &tc,
		func(*jwt.Token) (any, error) { return i.secret, nil },
		jwt.WithValidMethods([]string{signingAlg}),
		jwt.WithIssuer(issuer),
		jwt.WithExpirationRequired(),
		jwt.WithTimeFunc(i.now),
	)
	if err != nil {
		return Claims{}, fmt.Errorf("auth: verify token: %w", err)
	}
	id, err := uuid.Parse(tc.Subject)
	if err != nil || id == uuid.Nil {
		return Claims{}, errors.New("auth: verify token: invalid subject")
	}
	return Claims{UserID: id, Email: tc.Email}, nil
}
