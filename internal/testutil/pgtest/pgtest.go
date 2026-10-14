// Package pgtest gives tests a Postgres database with the migrations applied
// and a pool connected as the tenantly_app role, so RLS is enforced.
//
// Two backends are supported:
//   - TENANTLY_TEST_DATABASE_URL: an existing server. The URL must be a
//     superuser. Each test gets a fresh database, dropped on cleanup.
//   - Otherwise embedded-postgres (Postgres 16). It downloads real binaries,
//     so Docker is not needed. Everything lives under <module root>/.tmp/embedded-pg
//     unless TENANTLY_EMBEDDED_PG_DIR overrides it.
//
// The migrations run once per server into a template database. Each test then
// creates its own database from that template, which is much cheaper than
// re-running every migration per test.
//
// If Postgres cannot start, the test is skipped with the reason. When
// TENANTLY_REQUIRE_DB=1 the same situation fails the test instead.
package pgtest

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	embeddedpostgres "github.com/fergusstrange/embedded-postgres"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib" // registers the "pgx" database/sql driver

	"github.com/iboondk/tenantly/migrations"
)

const (
	// EnvDatabaseURL names an existing superuser connection string.
	EnvDatabaseURL = "TENANTLY_TEST_DATABASE_URL"
	// EnvRequireDB makes a missing Postgres a failure instead of a skip.
	EnvRequireDB = "TENANTLY_REQUIRE_DB"
	// EnvEmbeddedDir overrides the embedded-postgres base directory. The
	// directory must be on a POSIX filesystem: initdb rejects the permissions
	// of exFAT and msdos volumes.
	EnvEmbeddedDir = "TENANTLY_EMBEDDED_PG_DIR"

	appRole        = "tenantly_app"
	embeddedUser   = "postgres"
	embeddedDB     = "postgres"
	startTimeout   = 60 * time.Second
	embeddedSubdir = ".tmp/embedded-pg"
)

// DB is a migrated test database.
type DB struct {
	// Superuser runs as the owner (postgres). It bypasses RLS and is used for
	// setup and for asserting state that RLS would hide.
	Superuser *sql.DB
	// App is a pool connected as tenantly_app. Use it for anything under test.
	App *pgxpool.Pool
	// AppURL is the connection string used by App.
	AppURL string
}

// server is a running Postgres instance. stop is nil for an external server.
type server struct {
	baseURL string // superuser URL; used to create and drop per-test databases
	stop    func() error

	tmplOnce sync.Once
	tmplName string // migrated template database, empty until first use
	tmplErr  error

	closeOnce sync.Once
	closeErr  error
}

var (
	sharedOnce sync.Once
	sharedSrv  *server
	sharedErr  error
)

// New starts a Postgres instance for this test, or uses TENANTLY_TEST_DATABASE_URL.
// The database is created fresh from the migrated template, and dropped on
// cleanup. Embedded Postgres is stopped on cleanup as well. Use Shared when
// many tests share one server.
func New(t testing.TB) *DB {
	t.Helper()
	srv, err := startServer()
	if err != nil {
		skipOrFatal(t, err)
		return nil
	}
	t.Cleanup(func() {
		if err := srv.close(); err != nil {
			t.Logf("pgtest: close postgres: %v", err)
		}
	})
	return provision(t, srv)
}

// Shared returns a fresh migrated database on a server that starts once per
// process. It is safe to call from many tests. The owning package must call
// StopShared from TestMain after m.Run.
func Shared(t testing.TB) *DB {
	t.Helper()
	sharedOnce.Do(func() {
		sharedSrv, sharedErr = startServer()
	})
	if sharedErr != nil {
		skipOrFatal(t, sharedErr)
		return nil
	}
	return provision(t, sharedSrv)
}

// StopShared drops the template and stops the server started by Shared. It is
// a no-op when Shared was never called.
func StopShared() error {
	if sharedSrv == nil {
		return nil
	}
	return sharedSrv.close()
}

func skipOrFatal(t testing.TB, err error) {
	t.Helper()
	if os.Getenv(EnvRequireDB) == "1" {
		t.Fatalf("postgres unavailable: %v", err)
	}
	t.Skipf("postgres unavailable: %v", err)
}

// startServer returns the external server from the environment, or starts embedded Postgres.
func startServer() (*server, error) {
	if raw := os.Getenv(EnvDatabaseURL); raw != "" {
		if err := pingURL(raw); err != nil {
			return nil, fmt.Errorf("%s: %w", EnvDatabaseURL, err)
		}
		return &server{baseURL: raw}, nil
	}
	return startEmbedded()
}

func startEmbedded() (*server, error) {
	base := os.Getenv(EnvEmbeddedDir)
	if base == "" {
		root, err := moduleRoot()
		if err != nil {
			return nil, err
		}
		base = filepath.Join(root, embeddedSubdir)
	}
	if err := os.MkdirAll(base, 0o755); err != nil {
		return nil, fmt.Errorf("create %s: %w", base, err)
	}
	if fs := filesystemType(base); fs == "exfat" || fs == "msdos" {
		return nil, fmt.Errorf("embedded postgres directory %s is on a %s volume, which has no POSIX permissions, so initdb refuses it. "+
			"Run the tests through make (it attaches a disk image at .tmp/pgvol), or set %s to a directory on a POSIX filesystem",
			base, fs, EnvEmbeddedDir)
	}
	removeStaleRuns(base)

	port, err := freePort()
	if err != nil {
		return nil, err
	}
	pw, err := randomHex(16)
	if err != nil {
		return nil, err
	}
	id, err := randomHex(6)
	if err != nil {
		return nil, err
	}
	runDir := filepath.Join(base, "run-"+id)
	dataDir := filepath.Join(base, "data-"+id)

	cfg := embeddedpostgres.DefaultConfig().
		Version(embeddedpostgres.V16).
		Port(uint32(port)).
		Username(embeddedUser).
		Password(pw).
		Database(embeddedDB).
		RuntimePath(runDir).
		DataPath(dataDir).
		BinariesPath(filepath.Join(base, "bin")).
		CachePath(filepath.Join(base, "cache")).
		StartTimeout(startTimeout).
		Logger(io.Discard)

	ep := embeddedpostgres.NewDatabase(cfg)
	if err := ep.Start(); err != nil {
		_ = os.RemoveAll(runDir)
		_ = os.RemoveAll(dataDir)
		return nil, fmt.Errorf("start embedded postgres 16 on port %d: %w", port, err)
	}

	srv := &server{
		baseURL: fmt.Sprintf("postgres://%s:%s@127.0.0.1:%d/%s?sslmode=disable",
			embeddedUser, pw, port, embeddedDB),
		stop: func() error {
			err := ep.Stop()
			_ = os.RemoveAll(runDir)
			_ = os.RemoveAll(dataDir)
			return err
		},
	}
	return srv, nil
}

// removeStaleRuns deletes data and run directories left behind by a test
// process that died before its cleanup ran, along with temp_* extraction
// directories. A data directory whose postmaster.pid names a live process is
// kept, together with its run directory. This assumes one test process at a
// time uses a given base directory, which the Makefile and go test -p 1 ensure.
func removeStaleRuns(base string) {
	entries, err := os.ReadDir(base)
	if err != nil {
		return
	}
	keep := map[string]bool{}
	anyLive := false
	for _, e := range entries {
		name := e.Name()
		if !e.IsDir() || !strings.HasPrefix(name, "data-") {
			continue
		}
		if pidFileAlive(filepath.Join(base, name, "postmaster.pid")) {
			keep[strings.TrimPrefix(name, "data-")] = true
			anyLive = true
		}
	}
	for _, e := range entries {
		name := e.Name()
		if !e.IsDir() {
			continue
		}
		switch {
		case strings.HasPrefix(name, "data-"):
			if !keep[strings.TrimPrefix(name, "data-")] {
				_ = os.RemoveAll(filepath.Join(base, name))
			}
		case strings.HasPrefix(name, "run-"):
			if !keep[strings.TrimPrefix(name, "run-")] {
				_ = os.RemoveAll(filepath.Join(base, name))
			}
		}
	}
	if anyLive {
		return
	}
	for _, pattern := range []string{"temp_*", "*/temp_*"} {
		matches, _ := filepath.Glob(filepath.Join(base, pattern))
		for _, m := range matches {
			_ = os.RemoveAll(m)
		}
	}
}

// pidFileAlive reports whether path is a postmaster.pid whose process still runs.
func pidFileAlive(path string) bool {
	raw, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	line, _, _ := strings.Cut(string(raw), "\n")
	pid, err := parsePID(strings.TrimSpace(line))
	if err != nil {
		return false
	}
	return processAlive(pid)
}

func parsePID(s string) (int, error) {
	var pid int
	if _, err := fmt.Sscanf(s, "%d", &pid); err != nil || pid <= 0 {
		return 0, fmt.Errorf("invalid pid %q", s)
	}
	return pid, nil
}

// template returns the name of the database that holds the migrated schema,
// creating it on first use. Migrations run once per server.
func (s *server) template(ctx context.Context) (string, error) {
	s.tmplOnce.Do(func() {
		suffix, err := randomHex(4)
		if err != nil {
			s.tmplErr = err
			return
		}
		name := "tenantly_tmpl_" + suffix

		admin, err := sql.Open("pgx", s.baseURL)
		if err != nil {
			s.tmplErr = fmt.Errorf("open admin connection: %w", err)
			return
		}
		defer admin.Close()
		if _, err := admin.ExecContext(ctx, "CREATE DATABASE "+pgx.Identifier{name}.Sanitize()); err != nil {
			s.tmplErr = fmt.Errorf("create template database: %w", err)
			return
		}

		tmplURL, err := withDatabase(s.baseURL, name)
		if err != nil {
			s.tmplErr = err
			return
		}
		// Only record the template once it is fully migrated. The connection is
		// closed before any test clones it, because CREATE DATABASE ... TEMPLATE
		// fails while the template has open sessions.
		if err := migrateTemplate(ctx, tmplURL); err != nil {
			_, _ = admin.ExecContext(ctx, "DROP DATABASE IF EXISTS "+pgx.Identifier{name}.Sanitize()+" WITH (FORCE)")
			s.tmplErr = fmt.Errorf("migrate template database: %w", err)
			return
		}
		s.tmplName = name
	})
	return s.tmplName, s.tmplErr
}

func migrateTemplate(ctx context.Context, dbURL string) error {
	db, err := sql.Open("pgx", dbURL)
	if err != nil {
		return err
	}
	defer db.Close()
	if err := db.PingContext(ctx); err != nil {
		return err
	}
	return migrations.Up(ctx, db)
}

// close drops the template database and stops embedded Postgres. It is
// idempotent.
func (s *server) close() error {
	s.closeOnce.Do(func() {
		var errs []error
		if s.tmplName != "" {
			if err := s.dropDatabase(s.tmplName); err != nil {
				errs = append(errs, fmt.Errorf("drop template: %w", err))
			}
		}
		if s.stop != nil {
			if err := s.stop(); err != nil {
				errs = append(errs, err)
			}
		}
		s.closeErr = errors.Join(errs...)
	})
	return s.closeErr
}

func (s *server) dropDatabase(name string) error {
	admin, err := sql.Open("pgx", s.baseURL)
	if err != nil {
		return err
	}
	defer admin.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_, err = admin.ExecContext(ctx, "DROP DATABASE IF EXISTS "+pgx.Identifier{name}.Sanitize()+" WITH (FORCE)")
	return err
}

// provision creates a fresh database from the server's migrated template, sets
// a random password for tenantly_app, and opens the app pool.
func provision(t testing.TB, srv *server) *DB {
	t.Helper()
	ctx := context.Background()

	tmpl, err := srv.template(ctx)
	if err != nil {
		t.Fatalf("pgtest: %v", err)
	}

	suffix, err := randomHex(8)
	if err != nil {
		t.Fatalf("pgtest: random name: %v", err)
	}
	dbName := "tenantly_test_" + suffix

	admin, err := sql.Open("pgx", srv.baseURL)
	if err != nil {
		t.Fatalf("pgtest: open admin connection: %v", err)
	}
	defer admin.Close()
	if _, err := admin.ExecContext(ctx, "CREATE DATABASE "+pgx.Identifier{dbName}.Sanitize()+
		" TEMPLATE "+pgx.Identifier{tmpl}.Sanitize()); err != nil {
		t.Fatalf("pgtest: create database %s from template: %v", dbName, err)
	}
	t.Cleanup(func() {
		if err := srv.dropDatabase(dbName); err != nil {
			t.Logf("pgtest: drop %s: %v", dbName, err)
		}
	})

	dbURL, err := withDatabase(srv.baseURL, dbName)
	if err != nil {
		t.Fatalf("pgtest: %v", err)
	}

	super, err := sql.Open("pgx", dbURL)
	if err != nil {
		t.Fatalf("pgtest: open superuser connection: %v", err)
	}
	t.Cleanup(func() { _ = super.Close() })
	if err := super.PingContext(ctx); err != nil {
		t.Fatalf("pgtest: ping superuser: %v", err)
	}

	pw, err := randomHex(16)
	if err != nil {
		t.Fatalf("pgtest: random password: %v", err)
	}
	// pw is hex, so it needs no quoting.
	if _, err := super.ExecContext(ctx, "ALTER ROLE "+appRole+" LOGIN PASSWORD '"+pw+"'"); err != nil {
		t.Fatalf("pgtest: set %s password: %v", appRole, err)
	}

	appURL, err := withUser(dbURL, appRole, pw)
	if err != nil {
		t.Fatalf("pgtest: %v", err)
	}
	pool, err := pgxpool.New(ctx, appURL)
	if err != nil {
		t.Fatalf("pgtest: create app pool: %v", err)
	}
	t.Cleanup(pool.Close)
	if err := pool.Ping(ctx); err != nil {
		t.Fatalf("pgtest: ping app pool: %v", err)
	}

	return &DB{Superuser: super, App: pool, AppURL: appURL}
}

func pingURL(raw string) error {
	db, err := sql.Open("pgx", raw)
	if err != nil {
		return err
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return db.PingContext(ctx)
}

func withDatabase(raw, name string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("parse database url: %w", err)
	}
	u.Path = "/" + name
	return u.String(), nil
}

func withUser(raw, user, password string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("parse database url: %w", err)
	}
	u.User = url.UserPassword(user, password)
	return u.String(), nil
}

// moduleRoot walks up from the working directory until it finds go.mod.
func moduleRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", errors.New("pgtest: go.mod not found above working directory")
		}
		dir = parent
	}
}

func freePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, fmt.Errorf("find free port: %w", err)
	}
	defer l.Close()
	addr, ok := l.Addr().(*net.TCPAddr)
	if !ok {
		return 0, errors.New("pgtest: unexpected listener address type")
	}
	return addr.Port, nil
}

func randomHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("random bytes: %w", err)
	}
	return hex.EncodeToString(b), nil
}
