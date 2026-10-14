// Package store holds the data access layer. Every query for a tenant-owned
// table runs inside db.WithTenant or db.WithUser, so Row-Level Security sees
// the tenant and user context of the transaction.
package store

import (
	"time"

	"github.com/google/uuid"
)

// Tenant is an isolated customer account.
type Tenant struct {
	ID        uuid.UUID
	Name      string
	CreatedAt time.Time
}

// Project belongs to exactly one tenant.
type Project struct {
	ID        uuid.UUID
	TenantID  uuid.UUID
	Name      string
	CreatedAt time.Time
}

// Membership links a user to a tenant with a role (owner, admin or member).
type Membership struct {
	TenantID uuid.UUID
	UserID   uuid.UUID
	Role     string
}

// User is a global account. Rows are visible only to the user themself and to
// members of the current tenant (see migration 00003).
type User struct {
	ID          uuid.UUID
	Email       string
	DisplayName string
}
