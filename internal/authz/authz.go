// Package authz checks that the caller is a member of a tenant and holds a
// role high enough for the action.
package authz

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/iboondk/tenantly/internal/auth"
	"github.com/iboondk/tenantly/internal/store"
)

// ErrNotMember means the caller has no membership in the tenant. The HTTP
// layer answers 404 so that tenant IDs cannot be enumerated.
var ErrNotMember = errors.New("authz: not a member")

// ErrForbidden means the caller is a member but the role is too low.
var ErrForbidden = errors.New("authz: forbidden")

// Roles in ascending order of privilege.
const (
	RoleMember = "member"
	RoleAdmin  = "admin"
	RoleOwner  = "owner"
)

// MembershipGetter reads a membership. *store.Store satisfies it.
type MembershipGetter interface {
	GetMembership(ctx context.Context, tenantID, userID uuid.UUID) (store.Membership, error)
}

// Authorizer makes membership and role decisions.
type Authorizer struct {
	m MembershipGetter
}

// New returns an Authorizer that reads memberships from m.
func New(m MembershipGetter) *Authorizer {
	return &Authorizer{m: m}
}

// Check returns the caller's membership if they hold at least the role need.
// The caller comes from the claims in ctx. Without claims the check fails
// closed with ErrNotMember.
func (a *Authorizer) Check(ctx context.Context, tenantID uuid.UUID, need string) (store.Membership, error) {
	claims, ok := auth.FromContext(ctx)
	if !ok {
		return store.Membership{}, ErrNotMember
	}
	m, err := a.m.GetMembership(ctx, tenantID, claims.UserID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return store.Membership{}, ErrNotMember
		}
		return store.Membership{}, fmt.Errorf("authz: get membership: %w", err)
	}
	if !RoleAtLeast(m.Role, need) {
		return store.Membership{}, ErrForbidden
	}
	return m, nil
}

// RoleAtLeast reports whether have is need or higher. Unknown roles rank
// below every known role, and an unknown need is never satisfied.
func RoleAtLeast(have, need string) bool {
	rn := roleRank(need)
	if rn == 0 {
		return false
	}
	return roleRank(have) >= rn
}

func roleRank(r string) int {
	switch r {
	case RoleMember:
		return 1
	case RoleAdmin:
		return 2
	case RoleOwner:
		return 3
	default:
		return 0
	}
}
