# ADR 0003: Redis for rate limiting and caching, with tenant-namespaced keys

- **Status:** Accepted

## Context

The API needs a rate limit that holds across instances, and a cache for project reads that reduces load on Postgres. Both use Redis. Both must respect tenant isolation: one tenant's cached data must never be served to another tenant, and one tenant's traffic must not use up another tenant's quota. Rate limiting and caching must also be testable without Docker.

## Decision

- **Rate limit algorithm.** A fixed-window counter in a single Lua script: `INCR` the key, and on the first hit set the expiry with `PEXPIRE`. The script runs atomically on the server, so concurrent API instances see one consistent count. The clock is injected, so tests control the window.
- **Rate limit key.** Keyed by tenant and user (`KeyByTenantUser`). Requests without authentication are keyed by client IP. The limit comes from `RATE_LIMIT_PER_MINUTE`.
- **Response.** A request over the limit gets **429 `rate_limited`** with `Retry-After`. Redis errors fail open: the request is served and the error is logged. This keeps the API up when Redis is down, and the trade-off is recorded in Consequences.
- **Cache.** Project reads go through `CachedProjects`, a decorator around the project service. Every key starts with `tenantly:t:{tenantID}:`. A cache read happens only after the membership check, so a caller who is not a member never reaches the cache. TTL comes from `CACHE_TTL`. Creating a project invalidates the list cache for that tenant.
- **Tests.** `miniredis` runs in process, supports Lua `EVAL`, and needs no Docker. A test checks that a project ID from tenant A, requested under tenant B, never returns A's cached entry.

## Consequences

- Fixed-window limiting is simple to reason about and test, but it allows bursts at a window boundary: up to twice the limit can pass across two adjacent windows.
- Failing open means that during a Redis outage the rate limit is not enforced. We accept this because an API outage is worse for tenants than a short period without rate limiting. Operators who need the opposite can change the middleware to fail closed. Alerts should cover the logged errors.
- Tenant-prefixed keys make isolation visible in the key space and easy to scan for in tests. They do not replace the membership check or the database policies (ADR 0001).
- Cache invalidation on create is simple for one tenant's list. Other cached shapes must be invalidated explicitly when they are added.
- Redis is an additional runtime dependency. Local runs can start without it only if the middleware is not wired in.

## Alternatives considered

- **Sliding window log or sliding window counter.** Smoother limits and no boundary bursts, but more Redis work per request and more code to test. Start with fixed window, and revisit if bursts matter in practice.
- **GCRA (generic cell rate algorithm), also called a leaky bucket.** Smooth and precise, with one timestamp per key. It is harder to explain to operators and to read in the Lua script. Possible later.
- **In-process limiter.** No network hop, but limits would be per instance, so the effective limit grows with the number of instances.
- **Cache in process memory.** Fast, but each instance would hold its own copy and invalidation would not reach the other instances.
