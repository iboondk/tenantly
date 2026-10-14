package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/iboondk/tenantly/internal/auth"
	"github.com/iboondk/tenantly/internal/authz"
	"github.com/iboondk/tenantly/internal/config"
	"github.com/iboondk/tenantly/internal/service"
	"github.com/iboondk/tenantly/internal/store"
	"github.com/iboondk/tenantly/internal/testutil/pgtest"
)

const testSecret = "0123456789abcdef0123456789abcdef0123"

// testAPI is one router wired to a migrated test database.
type testAPI struct {
	t       *testing.T
	router  http.Handler
	store   *store.Store
	secret  []byte
	userSub map[string]string
}

// newTestAPI builds the router for the given APP_ENV. The fake OAuth provider
// is served in-process from httptest.
func newTestAPI(t *testing.T, appEnv string) *testAPI {
	t.Helper()
	return newTestAPIWith(t, appEnv, nil)
}

// newTestAPIWith is newTestAPI with middlewares placed after Authenticate.
func newTestAPIWith(t *testing.T, appEnv string, mws []func(http.Handler) http.Handler) *testAPI {
	t.Helper()
	db := pgtest.Shared(t)
	st := store.New(db.App)
	secret := []byte(testSecret)
	iss := auth.NewIssuer(secret, time.Hour, time.Now)

	provider := newFakeOAuthProvider(t)
	oa := auth.NewOAuth(config.OAuthConfig{
		ClientID:     "client-id",
		ClientSecret: "client-secret",
		AuthURL:      provider.URL + "/authorize",
		TokenURL:     provider.URL + "/token",
		UserInfoURL:  provider.URL + "/userinfo",
		RedirectURL:  "http://localhost:8080/v1/auth/oauth/callback",
	}, provider.Client())

	router := NewRouter(Deps{
		Cfg:         config.Config{AppEnv: appEnv, JWTSecret: secret},
		Issuer:      iss,
		OAuth:       oa,
		Store:       st,
		Authz:       authz.New(st),
		Projects:    service.NewProjects(st),
		Middlewares: mws,
	})
	return &testAPI{t: t, router: router, store: st, secret: secret}
}

// newFakeOAuthProvider serves /token and /userinfo. Any code is accepted, and
// the userinfo document is derived from the code, which is "user:<email>".
func newFakeOAuthProvider(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			http.Error(w, "bad form", http.StatusBadRequest)
			return
		}
		code := r.Form.Get("code")
		if !strings.HasPrefix(code, "user:") {
			http.Error(w, `{"error":"invalid_grant"}`, http.StatusBadRequest)
			return
		}
		// The access token carries the email, so userinfo can answer for it.
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"at:` + strings.TrimPrefix(code, "user:") + `","token_type":"Bearer","expires_in":3600}`))
	})
	mux.HandleFunc("/userinfo", func(w http.ResponseWriter, r *http.Request) {
		email, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer at:")
		if !ok {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{
			"sub":   "oidc-" + email,
			"email": email,
			"name":  "OAuth " + email,
		})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// do sends a request to the router. A non-nil body is JSON-encoded. A non-empty
// token is sent as a Bearer header.
func (a *testAPI) do(method, path, token string, body any) *httptest.ResponseRecorder {
	a.t.Helper()
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			a.t.Fatalf("encode body: %v", err)
		}
	}
	req := httptest.NewRequest(method, path, &buf)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	a.router.ServeHTTP(rec, req)
	return rec
}

// devLogin returns a token for email through /v1/auth/dev-token.
func (a *testAPI) devLogin(email string) string {
	a.t.Helper()
	rec := a.do(http.MethodPost, "/v1/auth/dev-token", "", map[string]string{"email": email})
	if rec.Code != http.StatusOK {
		a.t.Fatalf("dev-token %s: status %d body %s", email, rec.Code, rec.Body)
	}
	return decodeToken(a.t, rec)
}

// userID returns the claims subject of token, which is the user ID.
func (a *testAPI) userID(token string) uuid.UUID {
	a.t.Helper()
	c, err := auth.NewIssuer(a.secret, time.Hour, time.Now).Verify(token)
	if err != nil {
		a.t.Fatalf("verify token: %v", err)
	}
	return c.UserID
}

func decodeToken(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var out struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil || out.Token == "" {
		t.Fatalf("no token in body %s: %v", rec.Body, err)
	}
	return out.Token
}

func decodeID(t *testing.T, rec *httptest.ResponseRecorder) uuid.UUID {
	t.Helper()
	var out struct {
		ID uuid.UUID `json:"id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil || out.ID == uuid.Nil {
		t.Fatalf("no id in body %s: %v", rec.Body, err)
	}
	return out.ID
}

func requireStatus(t *testing.T, rec *httptest.ResponseRecorder, want int) {
	t.Helper()
	if rec.Code != want {
		t.Fatalf("status = %d, want %d; body %s", rec.Code, want, rec.Body)
	}
}

func requireErrorCode(t *testing.T, rec *httptest.ResponseRecorder, want string) {
	t.Helper()
	var out struct {
		Error   string `json:"error"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("error body is not JSON: %s", rec.Body)
	}
	if out.Error != want || out.Message == "" {
		t.Fatalf("error = %+v, want code %q with a message", out, want)
	}
}

func TestAPIFullFlow(t *testing.T) {
	a := newTestAPI(t, "test")
	alice := a.devLogin("alice@example.test")
	bob := a.devLogin("bob@example.test")
	bobID := a.userID(bob)

	// Alice creates tenant A and a project in it.
	rec := a.do(http.MethodPost, "/v1/tenants", alice, map[string]string{"name": "Tenant A"})
	requireStatus(t, rec, http.StatusCreated)
	tenantA := decodeID(t, rec)

	rec = a.do(http.MethodPost, "/v1/tenants/"+tenantA.String()+"/projects", alice, map[string]string{"name": "alpha"})
	requireStatus(t, rec, http.StatusCreated)
	projectA := decodeID(t, rec)

	// Bob creates tenant B and has no access to A.
	rec = a.do(http.MethodPost, "/v1/tenants", bob, map[string]string{"name": "Tenant B"})
	requireStatus(t, rec, http.StatusCreated)
	tenantB := decodeID(t, rec)

	// Non-member: list of A is 404, not 403, so IDs cannot be enumerated.
	rec = a.do(http.MethodGet, "/v1/tenants/"+tenantA.String()+"/projects", bob, nil)
	requireStatus(t, rec, http.StatusNotFound)
	requireErrorCode(t, rec, "not_found")

	// Bob is a member of B, but A's project under B's path is a 404.
	rec = a.do(http.MethodGet, "/v1/tenants/"+tenantB.String()+"/projects/"+projectA.String(), bob, nil)
	requireStatus(t, rec, http.StatusNotFound)
	requireErrorCode(t, rec, "not_found")

	// Bob's own tenant B works, so the 404 above is a scope result, not a broken route.
	rec = a.do(http.MethodGet, "/v1/tenants/"+tenantB.String()+"/projects", bob, nil)
	requireStatus(t, rec, http.StatusOK)

	// Alice adds Bob as a plain member of A through the store.
	if err := a.store.AddMember(t.Context(), tenantA, bobID, "member"); err != nil {
		t.Fatalf("AddMember: %v", err)
	}

	// A member can read...
	rec = a.do(http.MethodGet, "/v1/tenants/"+tenantA.String()+"/projects/"+projectA.String(), bob, nil)
	requireStatus(t, rec, http.StatusOK)
	rec = a.do(http.MethodGet, "/v1/tenants/"+tenantA.String()+"/projects", bob, nil)
	requireStatus(t, rec, http.StatusOK)

	// ...but a plain member cannot create. The role test: 403.
	rec = a.do(http.MethodPost, "/v1/tenants/"+tenantA.String()+"/projects", bob, map[string]string{"name": "beta"})
	requireStatus(t, rec, http.StatusForbidden)
	requireErrorCode(t, rec, "forbidden")

	// Unknown project ID inside the caller's own tenant is 404.
	rec = a.do(http.MethodGet, "/v1/tenants/"+tenantA.String()+"/projects/"+uuid.New().String(), alice, nil)
	requireStatus(t, rec, http.StatusNotFound)

	// Malformed tenant and project IDs are 404.
	rec = a.do(http.MethodGet, "/v1/tenants/not-a-uuid/projects", alice, nil)
	requireStatus(t, rec, http.StatusNotFound)
	rec = a.do(http.MethodGet, "/v1/tenants/"+tenantA.String()+"/projects/nope", alice, nil)
	requireStatus(t, rec, http.StatusNotFound)

	// Duplicate project names in one tenant conflict.
	rec = a.do(http.MethodPost, "/v1/tenants/"+tenantA.String()+"/projects", alice, map[string]string{"name": "alpha"})
	requireStatus(t, rec, http.StatusConflict)
	requireErrorCode(t, rec, "conflict")

	// Validation: blank name is 400.
	rec = a.do(http.MethodPost, "/v1/tenants/"+tenantA.String()+"/projects", alice, map[string]string{"name": "   "})
	requireStatus(t, rec, http.StatusBadRequest)
	requireErrorCode(t, rec, "invalid")

	// Unknown JSON fields are rejected.
	rec = a.do(http.MethodPost, "/v1/tenants/"+tenantA.String()+"/projects", alice, map[string]any{"name": "gamma", "extra": 1})
	requireStatus(t, rec, http.StatusBadRequest)

	// Limit must be an integer.
	rec = a.do(http.MethodGet, "/v1/tenants/"+tenantA.String()+"/projects?limit=abc", alice, nil)
	requireStatus(t, rec, http.StatusBadRequest)
}

func TestAPINoTokenIs401(t *testing.T) {
	a := newTestAPI(t, "test")
	rec := a.do(http.MethodGet, "/v1/tenants/"+uuid.New().String()+"/projects", "", nil)
	requireStatus(t, rec, http.StatusUnauthorized)
	requireErrorCode(t, rec, "unauthorized")

	rec = a.do(http.MethodPost, "/v1/tenants", "", map[string]string{"name": "x"})
	requireStatus(t, rec, http.StatusUnauthorized)

	rec = a.do(http.MethodGet, "/v1/tenants/"+uuid.New().String()+"/projects", "not-a-jwt", nil)
	requireStatus(t, rec, http.StatusUnauthorized)
}

func TestAPIDevTokenDisabledInProd(t *testing.T) {
	a := newTestAPI(t, "prod")
	rec := a.do(http.MethodPost, "/v1/auth/dev-token", "", map[string]string{"email": "alice@example.test"})
	requireStatus(t, rec, http.StatusNotFound)
	requireErrorCode(t, rec, "not_found")
}

func TestAPIDevTokenRejectsBadEmail(t *testing.T) {
	a := newTestAPI(t, "dev")
	for _, body := range []any{
		map[string]string{"email": ""},
		map[string]string{"email": "no-at-sign"},
		map[string]any{"email": "a@example.test", "role": "owner"},
	} {
		rec := a.do(http.MethodPost, "/v1/auth/dev-token", "", body)
		requireStatus(t, rec, http.StatusBadRequest)
	}
}

func TestAPIOAuthLoginCallbackToken(t *testing.T) {
	a := newTestAPI(t, "test")

	// Login redirects to the provider and sets the state cookie.
	rec := a.do(http.MethodGet, "/v1/auth/oauth/login", "", nil)
	requireStatus(t, rec, http.StatusFound)
	loc, err := url.Parse(rec.Header().Get("Location"))
	if err != nil {
		t.Fatalf("Location: %v", err)
	}
	state := loc.Query().Get("state")
	if len(state) < 40 {
		t.Fatalf("state too short: %q", state)
	}
	var stateCookie *http.Cookie
	for _, c := range rec.Result().Cookies() {
		if c.Name == "oauth_state" {
			stateCookie = c
		}
	}
	if stateCookie == nil || !stateCookie.HttpOnly || stateCookie.SameSite != http.SameSiteLaxMode || stateCookie.Value != state {
		t.Fatalf("state cookie = %+v, want HttpOnly Lax with value == state", stateCookie)
	}

	// A callback with the wrong state is refused.
	cb := httptest.NewRequest(http.MethodGet, "/v1/auth/oauth/callback?code=user:carol@example.test&state=wrong", nil)
	cb.AddCookie(stateCookie)
	rec2 := httptest.NewRecorder()
	a.router.ServeHTTP(rec2, cb)
	requireStatus(t, rec2, http.StatusBadRequest)
	requireErrorCode(t, rec2, "invalid_state")

	// The callback with the right state and cookie returns a working token.
	cb = httptest.NewRequest(http.MethodGet, "/v1/auth/oauth/callback?code=user:carol@example.test&state="+state, nil)
	cb.AddCookie(stateCookie)
	rec3 := httptest.NewRecorder()
	a.router.ServeHTTP(rec3, cb)
	requireStatus(t, rec3, http.StatusOK)
	tok := decodeToken(t, rec3)

	rec4 := a.do(http.MethodPost, "/v1/tenants", tok, map[string]string{"name": "OAuth Co"})
	requireStatus(t, rec4, http.StatusCreated)

	// Without the cookie the callback is refused.
	cb = httptest.NewRequest(http.MethodGet, "/v1/auth/oauth/callback?code=user:carol@example.test&state="+state, nil)
	rec5 := httptest.NewRecorder()
	a.router.ServeHTTP(rec5, cb)
	requireStatus(t, rec5, http.StatusBadRequest)
}

func TestAPIOAuthRoutesAbsentWithoutProvider(t *testing.T) {
	a := newTestAPI(t, "test")
	// The routes are mounted only when Deps.OAuth is set. This router has no
	// OAuth, so the path falls through to the JSON 404.
	r := NewRouter(Deps{Cfg: config.Config{AppEnv: "test"}, Issuer: auth.NewIssuer([]byte(testSecret), time.Hour, time.Now), Store: a.store, Authz: authz.New(a.store), Projects: service.NewProjects(a.store)})
	req := httptest.NewRequest(http.MethodGet, "/v1/auth/oauth/login", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	requireStatus(t, rec, http.StatusNotFound)
}
