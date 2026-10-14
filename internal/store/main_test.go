package store

import (
	"fmt"
	"os"
	"testing"

	"github.com/iboondk/tenantly/internal/testutil/pgtest"
)

// TestMain stops the embedded Postgres started by pgtest.Shared once all tests
// in this package have finished.
func TestMain(m *testing.M) {
	code := m.Run()
	if err := pgtest.StopShared(); err != nil {
		fmt.Fprintf(os.Stderr, "store: stop shared postgres: %v\n", err)
	}
	os.Exit(code)
}
