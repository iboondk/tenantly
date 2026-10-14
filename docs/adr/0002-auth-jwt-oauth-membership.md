# ADR 0002: Authentication with JWT (HS256) and OAuth, authorization by membership

- **Status:** Accepted

## Context

The API needs to identify the caller on every request, and then decide whether that caller may act for a given tenant. Identity comes from an external OAuth2 provider when one is configured. Local development and load tests need to sign in without a provider. Authorization has to follow tenant membership and role, and must not reveal whether a tenant exists to someone outside it.

## Decision

- **Tokens.** The API issues its own JWTs with `github.com/golang-jwt/jwt/v5` using HS256. The verifier pins the algorithm, so tokens signed with `none` or any other algorithm are rejected. The secret comes from `JWT_SECRET`. Its lifetime comes from `JWT_TTL`.
- **OAuth.** `golang.org/x/oauth2` with a generic provider. The authorization, token and userinfo URLs come from the environment, so any OAuth2 provider works. The login endpoint sets an `oauth_state` cookie, and the callback checks it before exchanging the code. After the exchange, the userinfo fields `sub`, `email` and `name` are read and the user is upserted through `auth_upsert_user`. Tests use a fake `httptest` provider.
- **Dev sign-in.** `POST /v1/auth/dev-token` takes an email and returns a token. It is only registered when `APP_ENV` is `dev` or `test`. In any other environment the route does not exist and returns 404.
- **Membership and roles.** A tenant has memberships with the roles `member`, `admin` and `owner`, ordered `member < admin < owner`. `RequireMembership(role)` checks the caller's membership of `{tenantID}` in the path and that the role is high enough. Reads need `member`. Creating a project needs `admin`. The creator of a tenant becomes its `owner`.
- **Error mapping.** A caller who is not a member gets **404 `not_found`**, the same as for a missing resource, so tenant IDs cannot be enumerated. A member whose role is too low gets **403 `forbidden`**.

## Consequences

- HS256 needs one shared secret. Every service that verifies tokens must hold it, so this design fits a single API service. It is simple, has no network dependency, and is easy to test.
- Moving to RS256 with a JWKS endpoint would let other services verify tokens without the signing secret. That change is planned and recorded here. Verification code should keep the algorithm pinned when it changes.
- Because non-members get 404, the same response is returned for "tenant does not exist" and "you are not a member". Clients cannot probe tenant IDs.
- The dev-token route is safe only while `APP_ENV` is set correctly. The configuration must not allow `dev` or `test` in production.
- Authorization is done in the application (`authz.Check` and `RequireMembership`). The database scopes rows to a tenant (ADR 0001), but it cannot decide membership by itself.

## Alternatives considered

- **RS256 or JWKS from the start.** Better for multiple verifiers, but it adds key management and a JWKS endpoint that the project does not need yet. Deferred, not rejected.
- **Opaque session tokens stored in Redis or Postgres.** Revocation is immediate, but every request needs a lookup. JWTs with a short TTL were chosen for simplicity.
- **Authorization only inside the database.** Roles would have to be encoded in session settings or policies, and "is this user a member" would become part of every policy. Keeping the role decision in the application keeps the policies simple (ADR 0001).
