// Command migrate runs goose migrations against MIGRATE_DATABASE_URL.
//
// Usage: migrate up | down | status
package main

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"os"

	_ "github.com/jackc/pgx/v5/stdlib" // registers the "pgx" database/sql driver

	"github.com/iboondk/tenantly/migrations"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		log.Fatalf("migrate: %v", err)
	}
}

func run(args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("usage: migrate up|down|status")
	}

	dsn := os.Getenv("MIGRATE_DATABASE_URL")
	if dsn == "" {
		return fmt.Errorf("MIGRATE_DATABASE_URL is not set")
	}

	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer db.Close()

	ctx := context.Background()
	switch args[0] {
	case "up":
		return migrations.Up(ctx, db)
	case "down":
		return migrations.Down(ctx, db)
	case "status":
		return migrations.Status(ctx, db)
	default:
		return fmt.Errorf("unknown command %q (want up|down|status)", args[0])
	}
}
