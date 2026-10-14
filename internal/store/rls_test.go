package store

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
)

// The RLS suite runs as tenantly_app, the role the API connects as. Each test
// proves that isolation holds in the database itself, independent of the Go
// code. Run with: make test-rls

const (
	sqlstateInsufficientPrivilege = "42501"
)

func TestRLS_ListOnlyOwnTenant(t *testing.T) {
	f := seedTwoTenants(t)
	ctx := context.Background()

	got, err := f.store.ListProjects(ctx, f.tenantA.ID, f.ownerA, 100)
	if err != nil {
		t.Fatalf("ListProjects tenant A: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("want exactly 1 project for tenant A, got %d", len(got))
	}
	if got[0].ID != f.projectA.ID || got[0].TenantID != f.tenantA.ID {
		t.Fatalf("tenant A list returned %+v, want project A", got[0])
	}
}

func TestRLS_GetOtherTenantProjectNotFound(t *testing.T) {
	f := seedTwoTenants(t)
	ctx := context.Background()

	_, err := f.store.GetProject(ctx, f.tenantA.ID, f.ownerA, f.projectB.ID)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetProject(tenant A, project of B): want ErrNotFound, got %v", err)
	}
}

func TestRLS_RawSelectWithoutTenantSeesNothing(t *testing.T) {
	f := seedTwoTenants(t)
	ctx := context.Background()

	tx, err := f.db.App.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	for _, table := range []string{"projects", "tenants", "memberships"} {
		var n int
		if err := tx.QueryRow(ctx, "SELECT count(*) FROM "+table).Scan(&n); err != nil {
			t.Fatalf("count %s without tenant: %v", table, err)
		}
		if n != 0 {
			t.Fatalf("count %s without tenant = %d, want 0", table, n)
		}
	}
}

func TestRLS_RawSelectAllRowsScopedToTenant(t *testing.T) {
	f := seedTwoTenants(t)
	ctx := context.Background()

	tx, err := f.db.App.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := setTenantLocal(ctx, tx, f.tenantA.ID); err != nil {
		t.Fatalf("set app.tenant_id: %v", err)
	}

	// No WHERE clause: RLS alone must limit the result to tenant A.
	rows, err := tx.Query(ctx, "SELECT tenant_id FROM projects")
	if err != nil {
		t.Fatalf("select projects: %v", err)
	}
	defer rows.Close()
	var seen []uuid.UUID
	for rows.Next() {
		var tid uuid.UUID
		if err := rows.Scan(&tid); err != nil {
			t.Fatalf("scan: %v", err)
		}
		seen = append(seen, tid)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}
	if len(seen) != 1 {
		t.Fatalf("want 1 row visible under tenant A, got %d", len(seen))
	}
	for _, tid := range seen {
		if tid != f.tenantA.ID {
			t.Fatalf("row with tenant_id %s visible under tenant A", tid)
		}
	}
}

func TestRLS_InsertForOtherTenantRejected(t *testing.T) {
	f := seedTwoTenants(t)
	ctx := context.Background()

	tx, err := f.db.App.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := setTenantLocal(ctx, tx, f.tenantA.ID); err != nil {
		t.Fatalf("set app.tenant_id: %v", err)
	}

	_, err = tx.Exec(ctx,
		"INSERT INTO projects (tenant_id, name) VALUES ($1, 'injected')",
		f.tenantB.ID,
	)
	if err == nil {
		t.Fatal("insert into tenant B while scoped to A succeeded; WITH CHECK must reject it")
	}
	requireSQLState(t, err, sqlstateInsufficientPrivilege)
}

func TestRLS_UpdateOtherTenantAffectsZeroRows(t *testing.T) {
	f := seedTwoTenants(t)
	ctx := context.Background()

	tx, err := f.db.App.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := setTenantLocal(ctx, tx, f.tenantA.ID); err != nil {
		t.Fatalf("set app.tenant_id: %v", err)
	}

	tag, err := tx.Exec(ctx,
		"UPDATE projects SET name = 'hijacked' WHERE id = $1",
		f.projectB.ID,
	)
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if n := tag.RowsAffected(); n != 0 {
		t.Fatalf("update of tenant B row affected %d rows, want 0", n)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}

	// Check the stored value through the superuser, which RLS does not filter.
	var name string
	if err := f.db.Superuser.QueryRowContext(ctx,
		"SELECT name FROM projects WHERE id = $1", f.projectB.ID,
	).Scan(&name); err != nil {
		t.Fatalf("superuser read: %v", err)
	}
	if name != f.projectB.Name {
		t.Fatalf("tenant B project name changed to %q, want %q", name, f.projectB.Name)
	}
}

func TestRLS_DeleteOtherTenantAffectsZeroRows(t *testing.T) {
	f := seedTwoTenants(t)
	ctx := context.Background()

	tx, err := f.db.App.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := setTenantLocal(ctx, tx, f.tenantA.ID); err != nil {
		t.Fatalf("set app.tenant_id: %v", err)
	}

	tag, err := tx.Exec(ctx, "DELETE FROM projects WHERE id = $1", f.projectB.ID)
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	if n := tag.RowsAffected(); n != 0 {
		t.Fatalf("delete of tenant B row affected %d rows, want 0", n)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}

	var n int
	if err := f.db.Superuser.QueryRowContext(ctx,
		"SELECT count(*) FROM projects WHERE id = $1", f.projectB.ID,
	).Scan(&n); err != nil {
		t.Fatalf("superuser count: %v", err)
	}
	if n != 1 {
		t.Fatalf("tenant B project was removed (count=%d), want 1", n)
	}
}

func TestRLS_UserIdentitiesDenied(t *testing.T) {
	f := seedTwoTenants(t)
	ctx := context.Background()

	var n int
	err := f.db.App.QueryRow(ctx, "SELECT count(*) FROM user_identities").Scan(&n)
	if err == nil {
		t.Fatalf("tenantly_app read user_identities (count=%d); the role must have no grant", n)
	}
	requireSQLState(t, err, sqlstateInsufficientPrivilege)
}

func TestRLS_AppRoleCannotBypass(t *testing.T) {
	f := seedTwoTenants(t)
	ctx := context.Background()

	var bypassRLS, super bool
	if err := f.db.App.QueryRow(ctx,
		"SELECT rolbypassrls, rolsuper FROM pg_roles WHERE rolname = current_user",
	).Scan(&bypassRLS, &super); err != nil {
		t.Fatalf("read role attributes: %v", err)
	}
	if bypassRLS {
		t.Fatal("tenantly_app has BYPASSRLS; it must not")
	}
	if super {
		t.Fatal("tenantly_app is a superuser; it must not be")
	}

	// With row_security = off, Postgres errors instead of silently filtering
	// when a policy applies. FORCE RLS keeps the policy applied for the owner
	// as well, so there is no way to turn isolation off from the session.
	tx, err := f.db.App.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, "SET LOCAL row_security = off"); err != nil {
		t.Fatalf("set row_security off: %v", err)
	}
	var n int
	err = tx.QueryRow(ctx, "SELECT count(*) FROM projects").Scan(&n)
	if err == nil {
		t.Fatalf("select with row_security=off returned %d rows; want SQLSTATE %s", n, sqlstateInsufficientPrivilege)
	}
	requireSQLState(t, err, sqlstateInsufficientPrivilege)
}

func TestRLS_UsersVisibleOnlyViaMembership(t *testing.T) {
	f := seedTwoTenants(t)
	ctx := context.Background()

	tx, err := f.db.App.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := setTenantLocal(ctx, tx, f.tenantA.ID); err != nil {
		t.Fatalf("set app.tenant_id: %v", err)
	}

	countUser := func(id uuid.UUID) int {
		t.Helper()
		var n int
		if err := tx.QueryRow(ctx, "SELECT count(*) FROM users WHERE id = $1", id).Scan(&n); err != nil {
			t.Fatalf("count user: %v", err)
		}
		return n
	}

	// Positive control: owner A is a member of tenant A, so the row is visible.
	if n := countUser(f.ownerA); n != 1 {
		t.Fatalf("owner A visible under tenant A = %d rows, want 1", n)
	}
	// Owner B belongs only to tenant B, so under tenant A the row is hidden.
	if n := countUser(f.ownerB); n != 0 {
		t.Fatalf("owner B visible under tenant A = %d rows, want 0", n)
	}
}
