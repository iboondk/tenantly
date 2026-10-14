package auth

import (
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

var testSecret = []byte("0123456789abcdef0123456789abcdef")

func fixedClock(t time.Time) func() time.Time {
	return func() time.Time { return t }
}

func TestJWTRoundTrip(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	iss := NewIssuer(testSecret, time.Hour, fixedClock(now))
	id := uuid.New()

	tok, err := iss.Issue(Claims{UserID: id, Email: "alice@example.test"})
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	got, err := iss.Verify(tok)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if got.UserID != id || got.Email != "alice@example.test" {
		t.Fatalf("claims = %+v, want user %s and alice email", got, id)
	}
}

func TestJWTExpiredRejected(t *testing.T) {
	issued := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	tok, err := NewIssuer(testSecret, time.Minute, fixedClock(issued)).Issue(Claims{UserID: uuid.New()})
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	later := NewIssuer(testSecret, time.Minute, fixedClock(issued.Add(2*time.Minute)))
	if _, err := later.Verify(tok); err == nil {
		t.Fatal("expired token was accepted")
	}
}

func TestJWTWrongSecretRejected(t *testing.T) {
	now := time.Now()
	tok, err := NewIssuer(testSecret, time.Hour, fixedClock(now)).Issue(Claims{UserID: uuid.New()})
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	other := NewIssuer([]byte("another-secret-that-is-long-enough!!"), time.Hour, fixedClock(now))
	if _, err := other.Verify(tok); err == nil {
		t.Fatal("token signed with another secret was accepted")
	}
}

func TestJWTAlgNoneRejected(t *testing.T) {
	now := time.Now()
	iss := NewIssuer(testSecret, time.Hour, fixedClock(now))
	tc := tokenClaims{RegisteredClaims: jwt.RegisteredClaims{
		Subject:   uuid.New().String(),
		Issuer:    issuer,
		ExpiresAt: jwt.NewNumericDate(now.Add(time.Hour)),
	}}
	tok, err := jwt.NewWithClaims(jwt.SigningMethodNone, tc).SignedString(jwt.UnsafeAllowNoneSignatureType)
	if err != nil {
		t.Fatalf("sign none token: %v", err)
	}
	if _, err := iss.Verify(tok); err == nil {
		t.Fatal("alg none token was accepted")
	}
}

func TestJWTHS512Rejected(t *testing.T) {
	now := time.Now()
	iss := NewIssuer(testSecret, time.Hour, fixedClock(now))
	tc := tokenClaims{RegisteredClaims: jwt.RegisteredClaims{
		Subject:   uuid.New().String(),
		Issuer:    issuer,
		ExpiresAt: jwt.NewNumericDate(now.Add(time.Hour)),
	}}
	tok, err := jwt.NewWithClaims(jwt.SigningMethodHS512, tc).SignedString(testSecret)
	if err != nil {
		t.Fatalf("sign HS512 token: %v", err)
	}
	if _, err := iss.Verify(tok); err == nil {
		t.Fatal("HS512 token was accepted")
	}
}

func TestJWTWrongIssuerAndMissingExpRejected(t *testing.T) {
	now := time.Now()
	iss := NewIssuer(testSecret, time.Hour, fixedClock(now))

	wrongIss := tokenClaims{RegisteredClaims: jwt.RegisteredClaims{
		Subject:   uuid.New().String(),
		Issuer:    "someone-else",
		ExpiresAt: jwt.NewNumericDate(now.Add(time.Hour)),
	}}
	tok, err := jwt.NewWithClaims(jwt.SigningMethodHS256, wrongIss).SignedString(testSecret)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := iss.Verify(tok); err == nil {
		t.Fatal("token with wrong issuer was accepted")
	}

	noExp := tokenClaims{RegisteredClaims: jwt.RegisteredClaims{
		Subject: uuid.New().String(),
		Issuer:  issuer,
	}}
	tok, err = jwt.NewWithClaims(jwt.SigningMethodHS256, noExp).SignedString(testSecret)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := iss.Verify(tok); err == nil {
		t.Fatal("token without exp was accepted")
	}
}

func TestJWTTamperedPayloadRejected(t *testing.T) {
	now := time.Now()
	iss := NewIssuer(testSecret, time.Hour, fixedClock(now))
	tok, err := iss.Issue(Claims{UserID: uuid.New(), Email: "a@example.test"})
	if err != nil {
		t.Fatal(err)
	}
	forged := tokenClaims{RegisteredClaims: jwt.RegisteredClaims{
		Subject:   uuid.New().String(),
		Issuer:    issuer,
		ExpiresAt: jwt.NewNumericDate(now.Add(time.Hour)),
	}}
	// Signed with the right key but a different payload than the original token's
	// signature covers: the original signature must not verify the swapped payload.
	raw, err := jwt.NewWithClaims(jwt.SigningMethodHS256, forged).SignedString([]byte("attacker-controlled-secret-value!!"))
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(tok, ".")
	forgedParts := strings.Split(raw, ".")
	bad := parts[0] + "." + forgedParts[1] + "." + parts[2]
	if _, err := iss.Verify(bad); err == nil {
		t.Fatal("token with swapped payload was accepted")
	}
}
