# Tenantly architecture

Tenantly is the core of a multi-tenant SaaS, written in Go with Postgres. Tenant isolation is enforced **inside the database** with Row-Level Security (RLS), so a bug in a handler cannot read another tenant's rows. Around that core sit JWT and OAuth login, membership and role checks, a Redis rate limiter and cache, a GraphQL gateway next to REST, OpenTelemetry traces, and a k6 load test.

Three artifacts back the design:

1. **An RLS test suite** showing that cross-tenant reads and writes fail at the database level.
2. **A k6 load test** whose results table in `loadtest/RESULTS.md` is generated from a real run. It is marked "to be measured" until one happens.
3. **ADRs** under `docs/adr/` recording each major decision.

---

## 1. Architecture

```
            ┌──────────────── otelhttp (trace per request) ────────────────┐
 client ──► │ chi router                                                    │
            │  /healthz                                                     │
            │  /v1/auth/*  (dev-token, oauth login/callback)                │
            │  ── Authenticate (JWT HS256) ──► RateLimit (Redis) ──┐        │
            │                                                      │        │
            │  /v1/tenants/{tenantID}/...  RequireMembership(role) │        │
            │       REST handlers ──┐                              │        │
            │  /graphql (gqlgen) ───┼─ resolvers call authz.Check ─┘        │
            │                       ▼                                       │
            │            service.Projects (interface)                       │
            │              └─ CachedProjects (Redis, tenant-namespaced keys)│
            │                   └─ store.Store                              │
            │                        └─ db.WithTenant(tx):                  │
            │                             set_config('app.tenant_id', ...,  │
            │                                        true)  -- tx-local     │
            └───────────────────────────────┬──────────────────────────────┘
                                            ▼
                Postgres 16. App connects as `tenantly_app` (NOSUPERUSER, NOBYPASSRLS).
                Tables have ENABLE + FORCE ROW LEVEL SECURITY.
                Policies: tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid
```

### Isolation in layers

1. **Database (the boundary that matters).** Every tenant-owned table has `ENABLE` and `FORCE ROW LEVEL SECURITY`. The policy compares `tenant_id` with the transaction-local setting `app.tenant_id`. If that setting is missing, `NULLIF(...)` returns NULL and the query sees zero rows. A missing setting never raises an error and never exposes all rows. `WITH CHECK` blocks writes that would put a row under another tenant.
2. **Application.** `RequireMembership` and `authz.Check` confirm that the caller is a member of `{tenantID}` and holds the required role. The database alone cannot know whether a user *should* act for a tenant; it only scopes rows to the tenant that was set.
3. **Cache.** Every key begins with `tenantly:t:{tenantID}:`. Cache reads happen only after the membership check. A test proves that a project ID from tenant A, requested under tenant B, never returns A's cached entry.

### Roles

- **Database roles.**
  - `postgres` (or another owner or superuser) runs migrations and owns the `SECURITY DEFINER` functions.
  - `tenantly_app` is what the API connects as. It has `NOSUPERUSER` and `NOBYPASSRLS`, and gets only the grants it needs.
- **Tenant roles.** `member < admin < owner`.
  - Any member can read.
  - `admin` or `owner` can create projects.
  - `owner` is assigned on tenant creation.

### Global data that RLS hides

- **`users`.** A row is visible only when it is the current user (`app.user_id`) or a member of the current tenant. The visibility check is a subquery on `memberships`, and that subquery is itself filtered by RLS.
- **`user_identities` (OAuth provider and subject).** The app role has no grant on this table.
- **Login upsert.** Login runs through `auth_upsert_user(...)`, a `SECURITY DEFINER` function with a pinned `search_path`. It is the only way to create or find a user before any tenant context exists.

## 2. Technology choices

| Concern | Choice | Why |
|---|---|---|
| HTTP router | `github.com/go-chi/chi/v5` | Uses stdlib `net/http`, middleware composes cleanly, and `otelhttp` and gqlgen mount directly |
| Postgres driver | `github.com/jackc/pgx/v5` (`pgxpool`) | Fast, native types, and supports a tracer hook |
| DB tracing | `github.com/exaring/otelpgx` | Adds a span to each query |
| Migrations | `github.com/pressly/goose/v3`, SQL files embedded with `embed.FS` | Plain SQL; one runner is shared by the CLI and the tests |
| Test Postgres | `github.com/fergusstrange/embedded-postgres` (default) or `TENANTLY_TEST_DATABASE_URL` | Tests run **without Docker**: the library downloads real Postgres binaries |
| UUIDs | `github.com/google/uuid` | |
| JWT | `github.com/golang-jwt/jwt/v5` with HS256 and a pinned algorithm | Simple and has no network dependency. Moving to RS256 or JWKS is noted in an ADR |
| OAuth | `golang.org/x/oauth2` with a generic provider (auth, token and userinfo URLs from env) | Works with any OAuth2 provider. Tests use a fake `httptest` provider |
| Redis | `github.com/redis/go-redis/v9` | |
| Redis in tests | `github.com/alicebob/miniredis/v2` | Runs in process, supports Lua `EVAL`, and needs no Docker |
| Rate limit | Fixed-window counter in one Lua script (`INCR` + `PEXPIRE`), keyed by tenant and user | Atomic and easy to reason about and test. The clock is injected |
| GraphQL | `github.com/99designs/gqlgen` | Schema-first with generated types. Resolvers reuse `service.Projects` and `authz.Check` |
| Tracing | `go.opentelemetry.io/otel` SDK, `otelhttp`, stdout or OTLP-gRPC exporter chosen by env | Local runs need no collector |
| Load test | k6 script plus `cmd/k6report` (turns summary JSON into a Markdown table) | The table is generated from the real `summary.json` only |
| Local infra | `docker-compose.yml` (postgres:16, redis:7) | Optional, because the tests do not need it |

**Secrets.** No real credentials go anywhere. `.env.example` holds placeholders only. The test harness creates a random password for each run.

## 3. Directory layout

```
tenantly/
  docs/architecture.md           this document
  go.mod                         module github.com/iboondk/tenantly (go 1.23+)
  Makefile                       build, vet, test, test-rls, migrate, gqlgen, k6, k6-report
  .env.example
  docker-compose.yml             postgres:16 + redis:7 (optional; not used by tests)
  deploy/postgres/init/01-app-role.sh   sets tenantly_app LOGIN + password from env
  gqlgen.yml
  cmd/
    api/main.go                  wires config → telemetry → pool → store → services → router
    migrate/main.go              goose up/down/status against MIGRATE_DATABASE_URL
    k6report/main.go             summary.json → markdown table rows
  migrations/
    migrations.go                //go:embed *.sql ; func Up(ctx, *sql.DB) error
    00001_roles.sql
    00002_core.sql
    00003_rls.sql
    00004_auth_functions.sql
  internal/
    config/       config.go (+_test)
    telemetry/    telemetry.go
    db/           pool.go, tenant.go (WithTenant / WithUser)
    store/        store.go, models.go, errors.go, rls_test.go, store_test.go, main_test.go
    testutil/pgtest/  pgtest.go  (embedded-postgres or env URL; runs migrations; app-role pool)
    auth/         jwt.go, oauth.go, context.go (+_test)
    authz/        authz.go (+_test)
    service/      projects.go, cached_projects.go (+_test)
    httpapi/      router.go, middleware.go, handlers_auth.go, handlers_tenants.go,
                  handlers_projects.go, errors.go (+_test)
    ratelimit/    limiter.go, middleware.go (+_test)
    cache/        cache.go (+_test)
    graph/        schema.graphqls, resolver.go, schema.resolvers.go, generated/, model/ (+_test)
  loadtest/
    k6/tenantly.js
    RESULTS.md                   results table (to be measured)
    results/.gitkeep
  docs/adr/
    0001-postgres-rls-tenant-isolation.md
    0002-auth-jwt-oauth-membership.md
    0003-redis-rate-limit-and-cache.md
    0004-graphql-beside-rest.md
    0005-opentelemetry.md
```

## 4. Data model (migrations)

- `tenants(id uuid pk, name text not null check (char_length(name) between 1 and 200), created_at timestamptz default now())`
- `users(id uuid pk default gen_random_uuid(), email text not null unique, display_name text not null default '', created_at)`
- `user_identities(provider text, subject text, user_id uuid not null references users on delete cascade, primary key(provider, subject))`
- `memberships(tenant_id uuid references tenants on delete cascade, user_id uuid references users on delete cascade, role text not null check (role in ('owner','admin','member')), created_at, primary key(tenant_id, user_id))`, with an index on `(user_id)`
- `projects(id uuid pk default gen_random_uuid(), tenant_id uuid not null references tenants on delete cascade, name text not null check (char_length(name) between 1 and 200), created_at, unique(tenant_id, name))`, with an index on `(tenant_id, created_at desc)`

The policies are in `00003_rls.sql`. Every table gets both `ENABLE` and `FORCE ROW LEVEL SECURITY`.

| Table | Policy | Grant to tenantly_app |
|---|---|---|
| tenants | `USING/WITH CHECK (id = current_tenant)` | SELECT, INSERT, UPDATE, DELETE |
| memberships | `tenant_id = current_tenant` | SELECT, INSERT, UPDATE, DELETE |
| projects | `tenant_id = current_tenant` | SELECT, INSERT, UPDATE, DELETE |
| users | `USING (id = current_user_id OR EXISTS (SELECT 1 FROM memberships m WHERE m.user_id = users.id))` | SELECT |
| user_identities | no policy (deny all) | none |

In the table, `current_tenant` stands for `NULLIF(current_setting('app.tenant_id', true), '')::uuid`, and `current_user_id` uses `app.user_id` in the same way.

`00004_auth_functions.sql` defines `auth_upsert_user(p_provider text, p_subject text, p_email text, p_name text) RETURNS uuid`. Its properties:

- It is `SECURITY DEFINER` with `SET search_path = public, pg_temp`.
- `EXECUTE` is revoked from `PUBLIC` and granted to `tenantly_app`.

## 5. HTTP surface

| Method | Path | Auth | Notes |
|---|---|---|---|
| GET | `/healthz` | none | `{"status":"ok"}` |
| POST | `/v1/auth/dev-token` | none, **only when `APP_ENV=dev` or `test`** (otherwise 404) | body `{"email"}` → `{"token"}`, used by k6 |
| GET | `/v1/auth/oauth/login` | none | 302 to the provider, sets the `oauth_state` cookie |
| GET | `/v1/auth/oauth/callback` | none | checks state, exchanges the code, reads userinfo `{sub,email,name}`, upserts the user, returns `{"token"}` |
| POST | `/v1/tenants` | JWT | body `{"name"}` → 201 `{id,name}`; the caller becomes owner |
| GET | `/v1/tenants/{tenantID}/projects` | JWT + member | list |
| POST | `/v1/tenants/{tenantID}/projects` | JWT + admin | body `{"name"}` → 201 |
| GET | `/v1/tenants/{tenantID}/projects/{projectID}` | JWT + member | 404 when the project is missing or belongs to another tenant |
| POST | `/graphql` | JWT | resolvers run `authz.Check` on the `tenantId` argument |

Error rules:

- Errors are JSON in the form `{"error":"<code>","message":"..."}`.
- A non-member gets **404 `not_found`**, so tenant IDs cannot be enumerated.
- A member whose role is too low gets **403 `forbidden`**.
- A rate-limited request gets **429 `rate_limited`** with `Retry-After`.

## 6. Testing strategy

- **Tests that need Postgres** call `pgtest.New(t)`.
  - The harness uses `TENANTLY_TEST_DATABASE_URL` (a superuser URL) when it is set. Otherwise it starts embedded Postgres 16 on a free port, with a data directory under `os.MkdirTemp`.
  - It runs the migrations as superuser, then `ALTER ROLE tenantly_app LOGIN PASSWORD '<random>'`, then returns a pool **connected as `tenantly_app`**.
  - If Postgres cannot start (for example, there is no network to download binaries), the test is **skipped** with the reason. Under `TENANTLY_REQUIRE_DB=1` the same situation is **fatal**.
- **Packages run serially** (`go test -p 1 ./...`), so concurrent embedded-postgres downloads cannot race.
- **Redis tests** use miniredis. They need no external service.
- **Skips are visible.** A skipped test is reported as skipped by `go test`; set `TENANTLY_REQUIRE_DB=1` in CI to turn any skip into a failure.

## 7. Tooling notes

- The tests need only Go. Docker, `psql`, `redis-server` and the goose/gqlgen CLIs are optional.
- goose and gqlgen run through `go run <module>@<version>` or as library imports.
- The RLS suite relies on embedded-postgres, so Docker is not needed. Point its runtime/cache dir at the gitignored `.tmp/` directory.
- The k6 run needs `k6` installed. `loadtest/RESULTS.md` stays "to be measured" until a run is done.
