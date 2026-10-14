package db

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const setScopeSQL = "SELECT set_config('app.tenant_id', $1, true), set_config('app.user_id', $2, true)"

// WithTenant runs fn in a transaction whose app.tenant_id and app.user_id are
// set with set_config(..., is_local=true). RLS policies read these values.
// The transaction commits when fn returns nil and rolls back otherwise.
// A uuid.Nil is passed as the empty string, which the policies treat as "no tenant".
func WithTenant(ctx context.Context, pool *pgxpool.Pool, tenantID uuid.UUID, userID uuid.UUID, fn func(pgx.Tx) error) error {
	return withScope(ctx, pool, idOrEmpty(tenantID), idOrEmpty(userID), fn)
}

// WithUser is like WithTenant but sets only app.user_id. app.tenant_id stays
// empty, so tenant-scoped tables return no rows.
func WithUser(ctx context.Context, pool *pgxpool.Pool, userID uuid.UUID, fn func(pgx.Tx) error) error {
	return withScope(ctx, pool, "", idOrEmpty(userID), fn)
}

func withScope(ctx context.Context, pool *pgxpool.Pool, tenant, user string, fn func(pgx.Tx) error) (err error) {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("db: begin: %w", err)
	}
	// Rollback after a successful Commit returns pgx.ErrTxClosed, which is
	// ignored. The deferred call also covers panics in fn.
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err = tx.Exec(ctx, setScopeSQL, tenant, user); err != nil {
		return fmt.Errorf("db: set scope: %w", err)
	}
	if err = fn(tx); err != nil {
		return err
	}
	if err = tx.Commit(ctx); err != nil {
		return fmt.Errorf("db: commit: %w", err)
	}
	return nil
}

func idOrEmpty(id uuid.UUID) string {
	if id == uuid.Nil {
		return ""
	}
	return id.String()
}
