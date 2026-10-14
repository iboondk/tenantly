package store

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/iboondk/tenantly/internal/testutil/pgtest"
)

// seeded is the common fixture: two tenants, each with an owner and one project.
type seeded struct {
	db       *pgtest.DB
	store    *Store
	ownerA   uuid.UUID
	ownerB   uuid.UUID
	tenantA  Tenant
	tenantB  Tenant
	projectA Project
	projectB Project
}

func seedTwoTenants(t *testing.T) seeded {
	t.Helper()
	ctx := context.Background()
	db := pgtest.Shared(t)
	s := New(db.App)

	ownerA := mustUpsertUser(t, s, "owner-a")
	ownerB := mustUpsertUser(t, s, "owner-b")

	tenantA, err := s.CreateTenant(ctx, ownerA, "Tenant A")
	if err != nil {
		t.Fatalf("seed tenant A: %v", err)
	}
	tenantB, err := s.CreateTenant(ctx, ownerB, "Tenant B")
	if err != nil {
		t.Fatalf("seed tenant B: %v", err)
	}
	projectA, err := s.CreateProject(ctx, tenantA.ID, ownerA, "project-a")
	if err != nil {
		t.Fatalf("seed project A: %v", err)
	}
	projectB, err := s.CreateProject(ctx, tenantB.ID, ownerB, "project-b")
	if err != nil {
		t.Fatalf("seed project B: %v", err)
	}

	return seeded{
		db:       db,
		store:    s,
		ownerA:   ownerA,
		ownerB:   ownerB,
		tenantA:  tenantA,
		tenantB:  tenantB,
		projectA: projectA,
		projectB: projectB,
	}
}

// mustUpsertUser creates an OAuth user whose subject and email are derived from label.
func mustUpsertUser(t *testing.T, s *Store, label string) uuid.UUID {
	t.Helper()
	id, err := s.UpsertOAuthUser(context.Background(), "test", label, label+"@example.test", "User "+label)
	if err != nil {
		t.Fatalf("upsert user %s: %v", label, err)
	}
	return id
}

// requireSQLState fails the test unless err carries the given SQLSTATE code.
func requireSQLState(t *testing.T, err error, code string) {
	t.Helper()
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		t.Fatalf("want SQLSTATE %s, got non-Postgres error: %v", code, err)
	}
	if pgErr.Code != code {
		t.Fatalf("want SQLSTATE %s, got %s: %s", code, pgErr.Code, pgErr.Message)
	}
}

// setTenantLocal sets app.tenant_id for the current transaction only.
func setTenantLocal(ctx context.Context, tx interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
}, tenantID uuid.UUID) error {
	_, err := tx.Exec(ctx, "SELECT set_config('app.tenant_id', $1, true)", tenantID.String())
	return err
}
