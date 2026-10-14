package store

import (
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgconn"
)

// ErrNotFound is returned when a query matches no row. Rows that belong to
// another tenant also produce ErrNotFound, because RLS hides them.
var ErrNotFound = errors.New("not found")

// ErrConflict is returned when a unique constraint is violated (SQLSTATE 23505).
var ErrConflict = errors.New("conflict")

const pgUniqueViolation = "23505"

// mapErr converts Postgres unique violations to ErrConflict. The original
// error stays in the chain so callers can still inspect it.
func mapErr(err error) error {
	if err == nil {
		return nil
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == pgUniqueViolation {
		return fmt.Errorf("%w: %w", ErrConflict, err)
	}
	return err
}
