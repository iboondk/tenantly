# ADR 0001: Tenant isolation with Postgres Row-Level Security

- **Status:** Accepted

## Context

Tenantly is a multi-tenant API. A bug in one handler, resolver or query must not expose another tenant's rows. Application-level filtering depends on every code path remembering the tenant condition, so one missed `WHERE` clause leaks data. We want the database to enforce the boundary, so that the enforcement does not depend on each query being written correctly.

## Decision

- Every tenant-owned table has `ENABLE ROW LEVEL SECURITY` and `FORCE ROW LEVEL SECURITY`. `FORCE` makes the policy apply to the table owner too.
- Each policy compares `tenant_id` with `NULLIF(current_setting('app.tenant_id', true), '')::uuid`. When the setting is missing or empty, `NULLIF` returns NULL and the query sees zero rows. A missing setting never raises an error and never exposes all rows.
- `WITH CHECK` on the same policy blocks inserts and updates that would place a row under another tenant.
- The API connects as `tenantly_app`, a role with `NOSUPERUSER` and `NOBYPASSRLS` and only the grants it needs. Migrations run as a separate owner role.
- The store sets the tenant with `set_config('app.tenant_id', $1, true)` inside each transaction. The third argument `true` makes the setting transaction-local, so it cannot leak to a pooled connection's next user.
- `users` has a policy that allows a row when it is the current user (`app.user_id`) or a member of the current tenant. `user_identities` has no policy and no grant to the app role, so OAuth subjects are not readable through the API. Login creates or finds users through `auth_upsert_user(...)`, a `SECURITY DEFINER` function with a pinned `search_path`, because no tenant context exists yet at that point.

## Consequences

- Cross-tenant reads and writes fail at the database level. The RLS test suite (`make test-rls`) checks this directly, and with `TENANTLY_REQUIRE_DB=1` a skipped run fails.
- A forgotten tenant condition in Go code returns fewer rows, not someone else's rows.
- The database cannot decide whether a user should act for a tenant. It only scopes rows to the tenant that was set. The application still checks membership and role (ADR 0002).
- Every request that touches tenant data needs a transaction with the setting applied. This adds a small amount of code to the store layer and makes it the one place where tenant context is set.
- Operators must keep the app role without `BYPASSRLS` and without table ownership. If either is granted, the policies no longer protect the data.
- Tests need a real Postgres. We use embedded Postgres so that no Docker is needed.

## Alternatives considered

- **Schema per tenant.** Gives a clear boundary, but migrations must run once per schema, connection pools and catalogs grow with the number of tenants, and cross-tenant admin queries become awkward.
- **Database per tenant.** The strongest separation, but the cost in connections, provisioning and operations is high for a service with many small tenants.
- **Application-only `WHERE tenant_id = ...` filters.** Simple, but correctness depends on every query. One missed filter leaks data, and nothing in the database catches it.
