package graph

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"

	"github.com/iboondk/tenantly/internal/auth"
	"github.com/iboondk/tenantly/internal/authz"
	"github.com/iboondk/tenantly/internal/config"
	"github.com/iboondk/tenantly/internal/httpapi"
	"github.com/iboondk/tenantly/internal/ratelimit"
	"github.com/iboondk/tenantly/internal/service"
	"github.com/iboondk/tenantly/internal/store"
	"github.com/iboondk/tenantly/internal/testutil/pgtest"
)

const graphTestSecret = "0123456789abcdef0123456789abcdef0123"

// gqlFixture is the full router (Authenticate, rate limit, GraphQL) on a
// migrated test database. Alice owns tenant A. Bob owns tenant B and has no
// role in A. Carol is a plain member of A.
type gqlFixture struct {
	t       *testing.T
	h       http.Handler
	iss     *auth.Issuer
	st      *store.Store
	alice   uuid.UUID
	bob     uuid.UUID
	carol   uuid.UUID
	tenantA uuid.UUID
	tenantB uuid.UUID
	projA   uuid.UUID
	aliceT  string
	bobT    string
	carolT  string
}

func newGQLFixture(t *testing.T) *gqlFixture {
	t.Helper()
	db := pgtest.Shared(t)
	ctx := context.Background()
	st := store.New(db.App)
	iss := auth.NewIssuer([]byte(graphTestSecret), time.Hour, time.Now)

	alice := mustUser(t, st, "alice@example.com", "Alice")
	bob := mustUser(t, st, "bob@example.com", "Bob")
	carol := mustUser(t, st, "carol@example.com", "Carol")

	tenantA, err := st.CreateTenant(ctx, alice, "Tenant A")
	if err != nil {
		t.Fatalf("create tenant A: %v", err)
	}
	tenantB, err := st.CreateTenant(ctx, bob, "Tenant B")
	if err != nil {
		t.Fatalf("create tenant B: %v", err)
	}
	if err := st.AddMember(ctx, tenantA.ID, carol, authz.RoleMember); err != nil {
		t.Fatalf("add carol to A: %v", err)
	}
	projA, err := service.NewProjects(st).Create(ctx, tenantA.ID, alice, "alpha")
	if err != nil {
		t.Fatalf("create project in A: %v", err)
	}

	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	limiter := ratelimit.New(rdb, 10000, time.Minute, time.Now)

	authorizer := authz.New(st)
	h := httpapi.NewRouter(httpapi.Deps{
		Cfg:      config.Config{AppEnv: "test", JWTSecret: []byte(graphTestSecret)},
		Issuer:   iss,
		Store:    st,
		Authz:    authorizer,
		Projects: service.NewProjects(st),
		Middlewares: []func(http.Handler) http.Handler{
			ratelimit.Middleware(limiter, ratelimit.KeyByTenantUser),
		},
		GraphQL: NewHandler(&Resolver{Store: st, Authz: authorizer, Projects: service.NewProjects(st)}, false),
	})

	return &gqlFixture{
		t: t, h: h, iss: iss, st: st,
		alice: alice, bob: bob, carol: carol,
		tenantA: tenantA.ID, tenantB: tenantB.ID, projA: projA.ID,
		aliceT: mustToken(t, iss, alice),
		bobT:   mustToken(t, iss, bob),
		carolT: mustToken(t, iss, carol),
	}
}

func mustUser(t *testing.T, st *store.Store, email, name string) uuid.UUID {
	t.Helper()
	id, err := st.UpsertOAuthUser(context.Background(), "test", "sub-"+email, email, name)
	if err != nil {
		t.Fatalf("upsert %s: %v", email, err)
	}
	return id
}

func mustToken(t *testing.T, iss *auth.Issuer, id uuid.UUID) string {
	t.Helper()
	tok, err := iss.Issue(auth.Claims{UserID: id})
	if err != nil {
		t.Fatalf("issue token: %v", err)
	}
	return tok
}

// gqlResponse is the decoded GraphQL response body.
type gqlResponse struct {
	Data   map[string]json.RawMessage `json:"data"`
	Errors []gqlErrorBody             `json:"errors"`
}

type gqlErrorBody struct {
	Message    string         `json:"message"`
	Path       []any          `json:"path"`
	Extensions map[string]any `json:"extensions"`
}

// post sends one GraphQL operation to /graphql. An empty token sends no
// Authorization header.
func (f *gqlFixture) post(token, query string, vars map[string]any) (int, gqlResponse) {
	f.t.Helper()
	body, err := json.Marshal(map[string]any{"query": query, "variables": vars})
	if err != nil {
		f.t.Fatalf("marshal request: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/graphql", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	f.h.ServeHTTP(rec, req)

	var out gqlResponse
	if rec.Code == http.StatusOK {
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			f.t.Fatalf("decode graphql response %q: %v", rec.Body.String(), err)
		}
	}
	return rec.Code, out
}

// fieldIsNull reports whether data.field is null or the data itself is null.
// A non-null root field that fails makes the whole data null.
func fieldIsNull(data map[string]json.RawMessage, field string) bool {
	if data == nil {
		return true
	}
	raw, ok := data[field]
	return !ok || string(raw) == "null"
}

func requireCode(t *testing.T, resp gqlResponse, want string) {
	t.Helper()
	if len(resp.Errors) == 0 {
		t.Fatalf("want error code %s, got no errors (data %v)", want, resp.Data)
	}
	got, _ := resp.Errors[0].Extensions["code"].(string)
	if got != want {
		t.Fatalf("want error code %s, got %q (message %q)", want, got, resp.Errors[0].Message)
	}
}

const projectsQuery = `query($t: UUID!) {
  projects(tenantId: $t) { id tenantId name createdAt }
}`

func TestGraphMemberListsProjects(t *testing.T) {
	f := newGQLFixture(t)
	code, resp := f.post(f.aliceT, projectsQuery, map[string]any{"t": f.tenantA})
	if code != http.StatusOK {
		t.Fatalf("status %d", code)
	}
	if len(resp.Errors) != 0 {
		t.Fatalf("unexpected errors: %+v", resp.Errors)
	}
	var out []struct {
		ID       uuid.UUID `json:"id"`
		TenantID uuid.UUID `json:"tenantId"`
		Name     string    `json:"name"`
	}
	if err := json.Unmarshal(resp.Data["projects"], &out); err != nil {
		t.Fatalf("decode projects: %v", err)
	}
	if len(out) != 1 || out[0].Name != "alpha" ||
		out[0].ID != f.projA || out[0].TenantID != f.tenantA {
		t.Fatalf("unexpected projects: %+v", out)
	}
}

func TestGraphCrossTenantListIsNotFound(t *testing.T) {
	f := newGQLFixture(t)
	code, resp := f.post(f.bobT, projectsQuery, map[string]any{"t": f.tenantA})
	if code != http.StatusOK {
		t.Fatalf("status %d", code)
	}
	requireCode(t, resp, "NOT_FOUND")
	if resp.Errors[0].Message != "not found" {
		t.Fatalf("message %q, want generic not found", resp.Errors[0].Message)
	}
	if !fieldIsNull(resp.Data, "projects") {
		t.Fatalf("data.projects should be null, got %v", resp.Data)
	}
}

func TestGraphCrossTenantProjectIsNull(t *testing.T) {
	f := newGQLFixture(t)
	// Bob is a member of tenant B. He asks for tenant A's project under B.
	const q = `query($t: UUID!, $id: UUID!) { project(tenantId: $t, id: $id) { id name } }`
	code, resp := f.post(f.bobT, q, map[string]any{"t": f.tenantB, "id": f.projA})
	if code != http.StatusOK {
		t.Fatalf("status %d", code)
	}
	if len(resp.Errors) != 0 {
		t.Fatalf("unexpected errors: %+v", resp.Errors)
	}
	if !fieldIsNull(resp.Data, "project") {
		t.Fatalf("data.project should be null, got %v", resp.Data)
	}
}

func TestGraphMemberCreateProjectIsForbidden(t *testing.T) {
	f := newGQLFixture(t)
	const m = `mutation($t: UUID!) { createProject(tenantId: $t, name: "nope") { id } }`
	code, resp := f.post(f.carolT, m, map[string]any{"t": f.tenantA})
	if code != http.StatusOK {
		t.Fatalf("status %d", code)
	}
	requireCode(t, resp, "FORBIDDEN")
	if !fieldIsNull(resp.Data, "createProject") {
		t.Fatalf("data.createProject should be null, got %v", resp.Data)
	}
}

func TestGraphNoTokenIs401(t *testing.T) {
	f := newGQLFixture(t)
	code, _ := f.post("", projectsQuery, map[string]any{"t": f.tenantA})
	if code != http.StatusUnauthorized {
		t.Fatalf("status %d, want 401", code)
	}
}

func TestGraphCreateTenantThenProjectRoundTrip(t *testing.T) {
	f := newGQLFixture(t)

	const mt = `mutation($n: String!) { createTenant(name: $n) { id name } }`
	code, resp := f.post(f.aliceT, mt, map[string]any{"n": "Globex"})
	if code != http.StatusOK || len(resp.Errors) != 0 {
		t.Fatalf("createTenant: status %d errors %+v", code, resp.Errors)
	}
	var tenant struct {
		ID   uuid.UUID `json:"id"`
		Name string    `json:"name"`
	}
	if err := json.Unmarshal(resp.Data["createTenant"], &tenant); err != nil {
		t.Fatalf("decode tenant: %v", err)
	}
	if tenant.Name != "Globex" || tenant.ID == uuid.Nil {
		t.Fatalf("unexpected tenant %+v", tenant)
	}

	const mp = `mutation($t: UUID!, $n: String!) {
  createProject(tenantId: $t, name: $n) { id tenantId name }
}`
	code, resp = f.post(f.aliceT, mp, map[string]any{"t": tenant.ID, "n": "roundtrip"})
	if code != http.StatusOK || len(resp.Errors) != 0 {
		t.Fatalf("createProject: status %d errors %+v", code, resp.Errors)
	}
	var proj struct {
		ID       uuid.UUID `json:"id"`
		TenantID uuid.UUID `json:"tenantId"`
		Name     string    `json:"name"`
	}
	if err := json.Unmarshal(resp.Data["createProject"], &proj); err != nil {
		t.Fatalf("decode project: %v", err)
	}
	if proj.Name != "roundtrip" || proj.TenantID != tenant.ID || proj.ID == uuid.Nil {
		t.Fatalf("unexpected project %+v", proj)
	}

	code, resp = f.post(f.aliceT, projectsQuery, map[string]any{"t": tenant.ID})
	if code != http.StatusOK || len(resp.Errors) != 0 {
		t.Fatalf("list: status %d errors %+v", code, resp.Errors)
	}
	var list []struct {
		ID uuid.UUID `json:"id"`
	}
	if err := json.Unmarshal(resp.Data["projects"], &list); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(list) != 1 || list[0].ID != proj.ID {
		t.Fatalf("round trip list mismatch: %+v", list)
	}
}

func TestGraphIntrospectionOffWhenDisabled(t *testing.T) {
	f := newGQLFixture(t)
	// The fixture builds the handler with introspection off.
	code, resp := f.post(f.aliceT, `{ __schema { queryType { name } } }`, nil)
	if code != http.StatusOK {
		t.Fatalf("status %d", code)
	}
	if len(resp.Errors) == 0 {
		t.Fatalf("expected introspection to be rejected, got data %v", resp.Data)
	}
}
