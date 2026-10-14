# Tenantly: tenant isolation in the database, not in the handlers

## Problem

In multi-tenant SaaS, one missing `WHERE tenant_id = ?` in one handler leaks another customer's data. Code review catches many such bugs, but every new endpoint or resolver is another chance to forget. Tenantly is the core of such a product, built so this bug class cannot expose rows.

## Architecture

A Go API using chi serves REST under `/v1/tenants/{tenantID}/...` and a gqlgen GraphQL endpoint at `/graphql`. Both share one authorizer and service. Requests pass JWT authentication (HS256, pinned algorithm), then a Redis fixed-window rate limiter keyed by tenant and user. Each database call runs in a transaction that sets `app.tenant_id` with a transaction-local `set_config`.

Postgres 16 holds the data. The API connects as `tenantly_app`, which is `NOSUPERUSER` and `NOBYPASSRLS`. Tenant tables use `ENABLE` and `FORCE ROW LEVEL SECURITY`, with a policy on `tenant_id`. Login uses a `SECURITY DEFINER` function, since no tenant exists yet at that point. Redis keys carry the tenant ID as a prefix.

## Key decisions

- **RLS as the boundary.** Postgres enforces isolation, so a handler bug cannot read another tenant's rows. The application still checks membership and role, because the database cannot know who should act for a tenant (ADR 0001).
- **Non-members get 404, not 403.** Tenant IDs cannot be enumerated. A member whose role is too low gets 403 (ADR 0002).
- **Fixed-window rate limit in one Lua script.** The count and expiry are atomic, and the clock is injected for tests (ADR 0003).
- **GraphQL beside REST.** Both use the same authorizer and service, so access rules cannot drift (ADR 0004).
- **OpenTelemetry from the start,** with stdout as the default exporter so local runs need no collector (ADR 0005).

## Results

How to check it:

- `go build ./...` and `go vet ./...`.
- `make test-rls` runs the 10 RLS tests; with `TENANTLY_REQUIRE_DB=1` any skip fails the run. They cover cross-tenant reads and writes, raw selects without tenant context, and denied identity access.
- `make test` runs every package.

Not yet measured: the k6 load test exists but has not been run against a live API. `loadtest/RESULTS.md` is marked "to be measured". This write-up contains no throughput or latency numbers, because none have been measured.

## Roadmap

1. Run k6 on a machine that has it installed, then fill in `RESULTS.md` from the generated report.
2. Add an API Dockerfile, so `docker compose up` starts the whole stack.
3. Move JWT signing from HS256 to RS256 or JWKS, so services can verify tokens without sharing a secret.
4. Add invitation and role-management endpoints. Memberships are created only by the system today.
5. Add a lint step to CI and a coverage report.
