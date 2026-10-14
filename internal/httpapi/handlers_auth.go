package httpapi

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/iboondk/tenantly/internal/auth"
)

const (
	oauthStateCookie = "oauth_state"
	oauthStateTTL    = 10 * time.Minute
	oauthStateBytes  = 32
	devProvider      = "dev"
	oauthProvider    = "oauth"
	maxEmailLen      = 254
)

// handlers holds the route implementations.

type handlers struct {
	d Deps
}

type tokenResponse struct {
	Token string `json:"token"`
}

// issueToken mints a token for userID and writes it as {"token": ...}.

// issueToken mints a token for userID and writes it as {"token": ...}.
func (h *handlers) issueToken(w http.ResponseWriter, userID uuid.UUID, email string) {
	tok, err := h.d.Issuer.Issue(auth.Claims{UserID: userID, Email: email})
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, tokenResponse{Token: tok})
}

// devToken signs in by email without a provider. It exists only in dev and test.

func (h *handlers) devToken(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Email string `json:"email"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	email := strings.ToLower(strings.TrimSpace(body.Email))
	if email == "" || len(email) > maxEmailLen || !strings.Contains(email, "@") {
		writeError(w, http.StatusBadRequest, "invalid", "a valid email is required")
		return
	}
	userID, err := h.d.Store.UpsertOAuthUser(r.Context(), devProvider, email, email, "")
	if err != nil {
		writeErr(w, err)
		return
	}
	h.issueToken(w, userID, email)
}

// oauthLogin sets a random state cookie and redirects to the provider.

func (h *handlers) oauthLogin(w http.ResponseWriter, r *http.Request) {
	buf := make([]byte, oauthStateBytes)
	if _, err := rand.Read(buf); err != nil {
		writeErr(w, err)
		return
	}
	state := base64.RawURLEncoding.EncodeToString(buf)
	http.SetCookie(w, h.stateCookie(state, int(oauthStateTTL.Seconds())))
	http.Redirect(w, r, h.d.OAuth.AuthCodeURL(state), http.StatusFound)
}

// oauthCallback checks the state against the cookie in constant time, then

func (h *handlers) oauthCallback(w http.ResponseWriter, r *http.Request) {
	cookie, err := r.Cookie(oauthStateCookie)
	// Clear the cookie on every path, so a state is single-use.
	http.SetCookie(w, h.stateCookie("", -1))
	if err != nil || cookie.Value == "" {
		writeError(w, http.StatusBadRequest, "invalid_state", "login state missing or expired")
		return
	}
	got := r.URL.Query().Get("state")
	if subtle.ConstantTimeCompare([]byte(got), []byte(cookie.Value)) != 1 {
		writeError(w, http.StatusBadRequest, "invalid_state", "login state mismatch")
		return
	}
	code := r.URL.Query().Get("code")
	if code == "" {
		writeError(w, http.StatusBadRequest, "invalid", "authorization code is required")
		return
	}

	ui, err := h.d.OAuth.Exchange(r.Context(), code)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "unauthorized", "oauth login failed")
		return
	}
	userID, err := h.d.Store.UpsertOAuthUser(r.Context(), oauthProvider, ui.Sub, ui.Email, ui.Name)
	if err != nil {
		writeErr(w, err)
		return
	}
	h.issueToken(w, userID, ui.Email)
}

// stateCookie builds the oauth_state cookie. A negative maxAge deletes it.

func (h *handlers) stateCookie(value string, maxAge int) *http.Cookie {
	return &http.Cookie{
		Name:     oauthStateCookie,
		Value:    value,
		Path:     "/v1/auth/oauth",
		MaxAge:   maxAge,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   !isDevEnv(h.d.Cfg.AppEnv),
	}
}
