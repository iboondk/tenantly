package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/iboondk/tenantly/internal/config"
)

// fakeProvider serves /authorize, /token and /userinfo. The userinfo answer
// is fixed, and it requires the bearer token issued by /token.
type fakeProvider struct {
	*httptest.Server
	userinfo map[string]string
}

func newFakeProvider(t *testing.T, userinfo map[string]string) *fakeProvider {
	t.Helper()
	fp := &fakeProvider{userinfo: userinfo}
	mux := http.NewServeMux()
	mux.HandleFunc("/authorize", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil || r.Form.Get("code") != "good-code" {
			http.Error(w, `{"error":"invalid_grant"}`, http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"fake-access","token_type":"Bearer","expires_in":3600}`))
	})
	mux.HandleFunc("/userinfo", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer fake-access" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(fp.userinfo)
	})
	fp.Server = httptest.NewServer(mux)
	t.Cleanup(fp.Close)
	return fp
}

func (fp *fakeProvider) config() config.OAuthConfig {
	return config.OAuthConfig{
		ClientID:     "client-id",
		ClientSecret: "client-secret",
		AuthURL:      fp.URL + "/authorize",
		TokenURL:     fp.URL + "/token",
		UserInfoURL:  fp.URL + "/userinfo",
		RedirectURL:  "http://localhost:8080/v1/auth/oauth/callback",
	}
}

func TestOAuthExchangeReturnsUserInfo(t *testing.T) {
	fp := newFakeProvider(t, map[string]string{
		"sub": "subject-123", "email": "carol@example.test", "name": "Carol",
	})
	o := NewOAuth(fp.config(), fp.Client())

	ui, err := o.Exchange(context.Background(), "good-code")
	if err != nil {
		t.Fatalf("Exchange: %v", err)
	}
	if ui.Sub != "subject-123" || ui.Email != "carol@example.test" || ui.Name != "Carol" {
		t.Fatalf("UserInfo = %+v", ui)
	}
}

func TestOAuthExchangeBadCodeFails(t *testing.T) {
	fp := newFakeProvider(t, map[string]string{"sub": "s", "email": "e@example.test"})
	o := NewOAuth(fp.config(), fp.Client())
	if _, err := o.Exchange(context.Background(), "bad-code"); err == nil {
		t.Fatal("Exchange accepted a bad code")
	}
}

func TestOAuthRejectsEmptySubOrEmail(t *testing.T) {
	cases := map[string]map[string]string{
		"empty sub":   {"sub": "", "email": "x@example.test"},
		"empty email": {"sub": "abc", "email": ""},
	}
	for name, ui := range cases {
		t.Run(name, func(t *testing.T) {
			fp := newFakeProvider(t, ui)
			o := NewOAuth(fp.config(), fp.Client())
			if _, err := o.Exchange(context.Background(), "good-code"); err == nil {
				t.Fatal("Exchange accepted userinfo without sub or email")
			}
		})
	}
}

func TestOAuthAuthCodeURLCarriesState(t *testing.T) {
	fp := newFakeProvider(t, map[string]string{"sub": "s", "email": "e@example.test"})
	o := NewOAuth(fp.config(), nil)
	raw := o.AuthCodeURL("state-xyz")
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(raw, fp.URL+"/authorize") {
		t.Fatalf("AuthCodeURL = %s", raw)
	}
	if u.Query().Get("state") != "state-xyz" || u.Query().Get("client_id") != "client-id" {
		t.Fatalf("query = %v", u.Query())
	}
}
