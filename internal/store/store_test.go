package store

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/iboondk/tenantly/internal/testutil/pgtest"
)

func TestCreateTenantMakesCallerOwner(t *testing.T) {
	db := pgtest.Shared(t)
	s := New(db.App)
	ctx := context.Background()

	owner := mustUpsertUser(t, s, "creator")
	tenant, err := s.CreateTenant(ctx, owner, "Acme")
	if err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	if tenant.ID == uuid.Nil || tenant.Name != "Acme" {
		t.Fatalf("unexpected tenant %+v", tenant)
	}

	m, err := s.GetMembership(ctx, tenant.ID, owner)
	if err != nil {
		t.Fatalf("GetMembership: %v", err)
	}
	if m.Role != "owner" {
		t.Fatalf("creator role = %q, want owner", m.Role)
	}
	if m.TenantID != tenant.ID || m.UserID != owner {
		t.Fatalf("membership %+v does not match tenant %s / user %s", m, tenant.ID, owner)
	}
}

func TestGetMembershipNonMemberNotFound(t *testing.T) {
	f := seedTwoTenants(t)
	ctx := context.Background()

	// ownerB is not a member of tenant A.
	_, err := f.store.GetMembership(ctx, f.tenantA.ID, f.ownerB)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetMembership(non-member): want ErrNotFound, got %v", err)
	}
}

func TestDuplicateProjectNameInSameTenantConflicts(t *testing.T) {
	f := seedTwoTenants(t)
	ctx := context.Background()

	if _, err := f.store.CreateProject(ctx, f.tenantA.ID, f.ownerA, "unique-name"); err != nil {
		t.Fatalf("first CreateProject: %v", err)
	}
	_, err := f.store.CreateProject(ctx, f.tenantA.ID, f.ownerA, "unique-name")
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate project name: want ErrConflict, got %v", err)
	}
}

func TestSameProjectNameInDifferentTenantsAllowed(t *testing.T) {
	f := seedTwoTenants(t)
	ctx := context.Background()

	if _, err := f.store.CreateProject(ctx, f.tenantA.ID, f.ownerA, "shared-name"); err != nil {
		t.Fatalf("CreateProject in tenant A: %v", err)
	}
	if _, err := f.store.CreateProject(ctx, f.tenantB.ID, f.ownerB, "shared-name"); err != nil {
		t.Fatalf("CreateProject in tenant B with the same name: %v", err)
	}
}

func TestUpsertOAuthUserIdempotent(t *testing.T) {
	db := pgtest.Shared(t)
	s := New(db.App)
	ctx := context.Background()

	first, err := s.UpsertOAuthUser(ctx, "github", "subject-42", "person@example.test", "Person")
	if err != nil {
		t.Fatalf("first upsert: %v", err)
	}
	second, err := s.UpsertOAuthUser(ctx, "github", "subject-42", "person@example.test", "Person")
	if err != nil {
		t.Fatalf("second upsert: %v", err)
	}
	if first != second {
		t.Fatalf("same provider+subject returned different users: %s vs %s", first, second)
	}
}
