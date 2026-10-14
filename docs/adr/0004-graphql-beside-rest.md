# ADR 0004: GraphQL served beside the REST API

- **Status:** Accepted

## Context

Some clients want to fetch a tenant's projects in one request with exactly the fields they need. The REST API stays the stable, documented surface. Adding GraphQL must not create a second place where access rules or business logic can drift apart.

## Decision

- **Schema first.** The schema lives in `internal/graph/schema.graphqls`. `gqlgen` generates the types, the executor and the resolver stubs. Generated code is checked in and regenerated with `go run github.com/99designs/gqlgen generate`.
- **Mounted beside REST.** `POST /graphql` sits next to `/v1/...` in the same chi router, behind the same `Authenticate` and rate-limit middleware, and inside the same `otelhttp` wrapper.
- **Shared service and authorization layer.** Resolvers call `service.Projects` and `authz.Check`, the same code as the REST handlers. A resolver never talks to the store directly, and it never reads a tenant ID without checking membership first.
- **Tenant argument checked.** Each query and mutation takes `tenantId` as an argument. The resolver runs `authz.Check` on it before any data is read, so a caller who is not a member of that tenant gets the same not-found result as REST.
- **Complexity limit.** The handler uses gqlgen's `FixedComplexityLimit` with a cap of 200 (`internal/graph/handler.go`), so one request cannot ask for an arbitrarily expensive graph.

## Consequences

- REST and GraphQL cannot disagree on who may see what. Both paths use one authorization function and one service.
- Adding a field or query means editing the schema and the resolver, then regenerating. Generated code can be large, and the diff of a regeneration must be reviewed.
- The complexity limit may reject some valid but expensive queries. Clients need to know the limit.
- Two surfaces to document and test. The resolver tests check that the cross-tenant query is denied, the same as the REST test.
- The GraphQL endpoint is not covered by the REST route list, so its tests and the load test (`graphql_projects` in `loadtest/k6/tenantly.js`) are kept separate.

## Alternatives considered

- **REST only.** The simplest option. The downside is over-fetching and many round trips for some clients. Clients that need GraphQL would need their own API layer.
- **Code-first GraphQL.** Types are written in Go and the schema is derived from them. This keeps everything in one language, but the public contract is harder to read and review. Schema first makes the contract visible in one file.
- **A separate GraphQL service.** Clean separation, but it duplicates authentication, rate limiting and tracing, and it creates a second copy of the authorization rules.
