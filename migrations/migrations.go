// Package migrations embeds the SQL migration files and runs them with goose.
package migrations

import (
	"context"
	"database/sql"
	"embed"
	"fmt"

	"github.com/pressly/goose/v3"
)

// FS holds the embedded *.sql migration files.
//
//go:embed *.sql
var FS embed.FS

func setup() error {
	goose.SetBaseFS(FS)
	if err := goose.SetDialect("postgres"); err != nil {
		return fmt.Errorf("migrations: set dialect: %w", err)
	}
	return nil
}

// Up applies all pending migrations.
func Up(ctx context.Context, db *sql.DB) error {
	if err := setup(); err != nil {
		return err
	}
	return goose.UpContext(ctx, db, ".")
}

// Down rolls back the most recent migration.
func Down(ctx context.Context, db *sql.DB) error {
	if err := setup(); err != nil {
		return err
	}
	return goose.DownContext(ctx, db, ".")
}

// Status prints the state of each migration to stdout.
func Status(ctx context.Context, db *sql.DB) error {
	if err := setup(); err != nil {
		return err
	}
	return goose.StatusContext(ctx, db, ".")
}
