package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"golang.org/x/oauth2"

	"github.com/iboondk/tenantly/internal/config"
)

const (
	maxUserInfoBytes   = 1 << 20
	defaultHTTPTimeout = 10 * time.Second
)

// UserInfo is the subset of the provider's userinfo document that Tenantly uses.
type UserInfo struct {
	Sub   string `json:"sub"`
	Email string `json:"email"`
	Name  string `json:"name"`
}

// OAuth runs the authorization-code flow against a generic provider.
type OAuth struct {
	cfg         *oauth2.Config
	userInfoURL string
	client      *http.Client
}

// NewOAuth builds the flow from c. A nil client gets a 10 second timeout.
func NewOAuth(c config.OAuthConfig, client *http.Client) *OAuth {
	if client == nil {
		client = &http.Client{Timeout: defaultHTTPTimeout}
	}
	return &OAuth{
		cfg: &oauth2.Config{
			ClientID:     c.ClientID,
			ClientSecret: c.ClientSecret,
			RedirectURL:  c.RedirectURL,
			Scopes:       []string{"openid", "email", "profile"},
			Endpoint: oauth2.Endpoint{
				AuthURL:   c.AuthURL,
				TokenURL:  c.TokenURL,
				AuthStyle: oauth2.AuthStyleAutoDetect,
			},
		},
		userInfoURL: c.UserInfoURL,
		client:      client,
	}
}

// AuthCodeURL returns the provider URL that starts the login, carrying state.
func (o *OAuth) AuthCodeURL(state string) string {
	return o.cfg.AuthCodeURL(state)
}

// Exchange trades code for an access token and then reads the userinfo
// document with it. It returns an error when sub or email is empty.
func (o *OAuth) Exchange(ctx context.Context, code string) (UserInfo, error) {
	ctx = context.WithValue(ctx, oauth2.HTTPClient, o.client)
	tok, err := o.cfg.Exchange(ctx, code)
	if err != nil {
		return UserInfo{}, fmt.Errorf("auth: oauth exchange: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, o.userInfoURL, nil)
	if err != nil {
		return UserInfo{}, fmt.Errorf("auth: userinfo request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+tok.AccessToken)
	req.Header.Set("Accept", "application/json")

	resp, err := o.client.Do(req)
	if err != nil {
		return UserInfo{}, fmt.Errorf("auth: userinfo: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return UserInfo{}, fmt.Errorf("auth: userinfo: status %d", resp.StatusCode)
	}

	var ui UserInfo
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxUserInfoBytes)).Decode(&ui); err != nil {
		return UserInfo{}, fmt.Errorf("auth: decode userinfo: %w", err)
	}
	if ui.Sub == "" || ui.Email == "" {
		return UserInfo{}, errors.New("auth: userinfo: sub and email are required")
	}
	return ui, nil
}
