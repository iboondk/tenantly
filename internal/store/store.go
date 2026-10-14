package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/iboondk/tenantly/internal/db"
)

const (
	minListLimit = 1
	maxListLimit = 100
)

// Store runs tenant-scoped queries. It never queries tenant tables directly
// through the pool.
type Store struct {
	pool *pgxpool.Pool
}

// New returns a Store backed by pool. The pool should connect as the
// tenantly_app role so that RLS applies.
func New(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

// UpsertOAuthUser finds or creates the user for an OAuth identity and returns
// the user ID. It calls auth_upsert_user, the SECURITY DEFINER function that
// runs before any tenant context exists.
func (s *Store) UpsertOAuthUser(ctx context.Context, provider, subject, email, name string) (uuid.UUID, error) {
	var id uuid.UUID
	err := db.WithUser(ctx, s.pool, uuid.Nil, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			"SELECT auth_upsert_user($1, $2, $3, $4)",
			provider, subject, email, name,
		).Scan(&id)
	})
	if err != nil {
		return uuid.Nil, fmt.Errorf("store: upsert oauth user: %w", mapErr(err))
	}
	return id, nil
}

// CreateTenant creates a tenant and makes ownerID its owner. Both inserts run
// in one transaction scoped to the new tenant.
func (s *Store) CreateTenant(ctx context.Context, ownerID uuid.UUID, name string) (Tenant, error) {
	newID := uuid.New()
	var t Tenant
	err := db.WithTenant(ctx, s.pool, newID, ownerID, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx,
			`INSERT INTO tenants (id, name) VALUES ($1, $2)
			 RETURNING id, name, created_at`,
			newID, name,
		).Scan(&t.ID, &t.Name, &t.CreatedAt); err != nil {
			return err
		}
		_, err := tx.Exec(ctx,
			`INSERT INTO memberships (tenant_id, user_id, role) VALUES ($1, $2, 'owner')`,
			newID, ownerID,
		)
		return err
	})
	if err != nil {
		return Tenant{}, fmt.Errorf("store: create tenant: %w", mapErr(err))
	}
	return t, nil
}

// GetMembership returns the caller's membership in a tenant, or ErrNotFound.
func (s *Store) GetMembership(ctx context.Context, tenantID, userID uuid.UUID) (Membership, error) {
	var m Membership
	err := db.WithTenant(ctx, s.pool, tenantID, userID, func(tx pgx.Tx) error {
		err := tx.QueryRow(ctx,
			`SELECT tenant_id, user_id, role FROM memberships
			 WHERE tenant_id = $1 AND user_id = $2`,
			tenantID, userID,
		).Scan(&m.TenantID, &m.UserID, &m.Role)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		return err
	})
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return Membership{}, ErrNotFound
		}
		return Membership{}, fmt.Errorf("store: get membership: %w", err)
	}
	return m, nil
}

// AddMember adds userID to tenantID with the given role. A duplicate returns
// ErrConflict. The role check constraint rejects unknown roles.
func (s *Store) AddMember(ctx context.Context, tenantID, userID uuid.UUID, role string) error {
	err := db.WithTenant(ctx, s.pool, tenantID, uuid.Nil, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx,
			`INSERT INTO memberships (tenant_id, user_id, role) VALUES ($1, $2, $3)`,
			tenantID, userID, role,
		)
		return err
	})
	if err != nil {
		return fmt.Errorf("store: add member: %w", mapErr(err))
	}
	return nil
}

// CreateProject creates a project in tenantID. A duplicate name within the
// same tenant returns ErrConflict.
func (s *Store) CreateProject(ctx context.Context, tenantID, userID uuid.UUID, name string) (Project, error) {
	var p Project
	err := db.WithTenant(ctx, s.pool, tenantID, userID, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			`INSERT INTO projects (tenant_id, name) VALUES ($1, $2)
			 RETURNING id, tenant_id, name, created_at`,
			tenantID, name,
		).Scan(&p.ID, &p.TenantID, &p.Name, &p.CreatedAt)
	})
	if err != nil {
		return Project{}, fmt.Errorf("store: create project: %w", mapErr(err))
	}
	return p, nil
}

// ListProjects returns up to limit projects of tenantID, newest first. The
// limit is clamped to the range 1..100, so a non-positive limit becomes 1.
func (s *Store) ListProjects(ctx context.Context, tenantID, userID uuid.UUID, limit int) ([]Project, error) {
	switch {
	case limit < minListLimit:
		limit = minListLimit
	case limit > maxListLimit:
		limit = maxListLimit
	}

	var out []Project
	err := db.WithTenant(ctx, s.pool, tenantID, userID, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx,
			`SELECT id, tenant_id, name, created_at FROM projects
			 WHERE tenant_id = $1
			 ORDER BY created_at DESC, id DESC
			 LIMIT $2`,
			tenantID, limit,
		)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var p Project
			if err := rows.Scan(&p.ID, &p.TenantID, &p.Name, &p.CreatedAt); err != nil {
				return err
			}
			out = append(out, p)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, fmt.Errorf("store: list projects: %w", err)
	}
	return out, nil
}

// GetProject returns one project of tenantID, or ErrNotFound. A project that
// belongs to another tenant is also ErrNotFound.
func (s *Store) GetProject(ctx context.Context, tenantID, userID, projectID uuid.UUID) (Project, error) {
	var p Project
	err := db.WithTenant(ctx, s.pool, tenantID, userID, func(tx pgx.Tx) error {
		err := tx.QueryRow(ctx,
			`SELECT id, tenant_id, name, created_at FROM projects
			 WHERE tenant_id = $1 AND id = $2`,
			tenantID, projectID,
		).Scan(&p.ID, &p.TenantID, &p.Name, &p.CreatedAt)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		return err
	})
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return Project{}, ErrNotFound
		}
		return Project{}, fmt.Errorf("store: get project: %w", err)
	}
	return p, nil
}
