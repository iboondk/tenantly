package authz

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/iboondk/tenantly/internal/auth"
	"github.com/iboondk/tenantly/internal/store"
)

// fakeGetter returns a fixed membership for one (tenant, user) pair and
// ErrNotFound for anything else.
type fakeGetter struct {
	tenant uuid.UUID
	user   uuid.UUID
	role   string
	err    error
}

func (f fakeGetter) GetMembership(_ context.Context, tenantID, userID uuid.UUID) (store.Membership, error) {
	if f.err != nil {
		return store.Membership{}, f.err
	}
	if tenantID != f.tenant || userID != f.user {
		return store.Membership{}, store.ErrNotFound
	}
	return store.Membership{TenantID: tenantID, UserID: userID, Role: f.role}, nil
}

func TestRoleAtLeast(t *testing.T) {
	cases := []struct {
		have, need string
		want       bool
	}{
		{"member", "member", true},
		{"admin", "member", true},
		{"owner", "member", true},
		{"member", "admin", false},
		{"admin", "admin", true},
		{"owner", "admin", true},
		{"member", "owner", false},
		{"admin", "owner", false},
		{"owner", "owner", true},
		{"", "member", false},
		{"superuser", "member", false},
		{"member", "", false},
		{"owner", "root", false},
	}
	for _, c := range cases {
		if got := RoleAtLeast(c.have, c.need); got != c.want {
			t.Errorf("RoleAtLeast(%q, %q) = %v, want %v", c.have, c.need, got, c.want)
		}
	}
}

func TestCheck(t *testing.T) {
	tenant := uuid.New()
	alice := uuid.New()
	stranger := uuid.New()

	cases := []struct {
		name    string
		getter  fakeGetter
		caller  uuid.UUID
		need    string
		wantErr error
		wantRol string
	}{
		{name: "not a member", getter: fakeGetter{tenant: tenant, user: alice, role: "owner"}, caller: stranger, need: "member", wantErr: ErrNotMember},
		{name: "member too low for admin", getter: fakeGetter{tenant: tenant, user: alice, role: "member"}, caller: alice, need: "admin", wantErr: ErrForbidden},
		{name: "member reads", getter: fakeGetter{tenant: tenant, user: alice, role: "member"}, caller: alice, need: "member", wantRol: "member"},
		{name: "admin creates", getter: fakeGetter{tenant: tenant, user: alice, role: "admin"}, caller: alice, need: "admin", wantRol: "admin"},
		{name: "owner passes admin", getter: fakeGetter{tenant: tenant, user: alice, role: "owner"}, caller: alice, need: "admin", wantRol: "owner"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ctx := auth.WithClaims(context.Background(), auth.Claims{UserID: c.caller})
			m, err := New(c.getter).Check(ctx, tenant, c.need)
			if c.wantErr != nil {
				if !errors.Is(err, c.wantErr) {
					t.Fatalf("err = %v, want %v", err, c.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Check: %v", err)
			}
			if m.Role != c.wantRol || m.TenantID != tenant || m.UserID != c.caller {
				t.Fatalf("membership = %+v", m)
			}
		})
	}
}

func TestCheckWithoutClaimsIsNotMember(t *testing.T) {
	tenant := uuid.New()
	g := fakeGetter{tenant: tenant, user: uuid.New(), role: "owner"}
	if _, err := New(g).Check(context.Background(), tenant, "member"); !errors.Is(err, ErrNotMember) {
		t.Fatalf("err = %v, want ErrNotMember", err)
	}
}

func TestCheckPassesThroughStoreErrors(t *testing.T) {
	boom := errors.New("connection reset")
	ctx := auth.WithClaims(context.Background(), auth.Claims{UserID: uuid.New()})
	_, err := New(fakeGetter{err: boom}).Check(ctx, uuid.New(), "member")
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want wrapped %v", err, boom)
	}
	if errors.Is(err, ErrNotMember) || errors.Is(err, ErrForbidden) {
		t.Fatalf("store failure must not become a membership decision: %v", err)
	}
}
