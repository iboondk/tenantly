package graph

import (
	"fmt"
	"os"
	"testing"

	"github.com/iboondk/tenantly/internal/testutil/pgtest"
)

// TestMain stops the Postgres started by pgtest.Shared after all tests run.
func TestMain(m *testing.M) {
	code := m.Run()
	if err := pgtest.StopShared(); err != nil {
		fmt.Fprintf(os.Stderr, "graph: stop shared postgres: %v\n", err)
	}
	os.Exit(code)
}
