package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"

	"github.com/iboondk/tenantly/internal/cache"
	"github.com/iboondk/tenantly/internal/store"
)

var errNotFound = errors.New("fake: not found")

// fakeProjects is an in-memory Projects that counts calls. It keeps projects
// per tenant, so a project ID from one tenant is not found under another.
type fakeProjects struct {
	byTenant    map[uuid.UUID]map[uuid.UUID]store.Project
	getCalls    int
	listCalls   int
	createCalls int
}

func newFakeProjects() *fakeProjects {
	return &fakeProjects{byTenant: map[uuid.UUID]map[uuid.UUID]store.Project{}}
}

func (f *fakeProjects) seed(tenant uuid.UUID, name string) store.Project {
	p := store.Project{ID: uuid.New(), TenantID: tenant, Name: name, CreatedAt: time.Unix(1700000000, 0).UTC()}
	if f.byTenant[tenant] == nil {
		f.byTenant[tenant] = map[uuid.UUID]store.Project{}
	}
	f.byTenant[tenant][p.ID] = p
	return p
}

func (f *fakeProjects) List(_ context.Context, tenantID, _ uuid.UUID, _ int) ([]store.Project, error) {
	f.listCalls++
	var out []store.Project
	for _, p := range f.byTenant[tenantID] {
		out = append(out, p)
	}
	return out, nil
}

func (f *fakeProjects) Get(_ context.Context, tenantID, _, projectID uuid.UUID) (store.Project, error) {
	f.getCalls++
	p, ok := f.byTenant[tenantID][projectID]
	if !ok {
		return store.Project{}, errNotFound
	}
	return p, nil
}

func (f *fakeProjects) Create(_ context.Context, tenantID, _ uuid.UUID, name string) (store.Project, error) {
	f.createCalls++
	return f.seed(tenantID, name), nil
}

func newCachedForTest(t *testing.T) (*miniredis.Miniredis, *fakeProjects, Projects) {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr(), MaxRetries: -1})
	t.Cleanup(func() { _ = rdb.Close() })
	inner := newFakeProjects()
	return mr, inner, NewCachedProjects(inner, cache.New(rdb, time.Minute))
}

func TestCachedGetSecondCallIsHit(t *testing.T) {
	_, inner, svc := newCachedForTest(t)
	ctx := context.Background()
	tenant, user := uuid.New(), uuid.New()
	p := inner.seed(tenant, "alpha")

	first, err := svc.Get(ctx, tenant, user, p.ID)
	if err != nil {
		t.Fatalf("first Get: %v", err)
	}
	second, err := svc.Get(ctx, tenant, user, p.ID)
	if err != nil {
		t.Fatalf("second Get: %v", err)
	}
	if inner.getCalls != 1 {
		t.Fatalf("inner Get called %d times, want 1 (second must be a cache hit)", inner.getCalls)
	}
	if first.ID != p.ID || second.ID != p.ID || second.Name != "alpha" {
		t.Fatalf("cached value mismatch: first=%+v second=%+v", first, second)
	}
}

func TestCachedGetDoesNotServeAnotherTenantsProject(t *testing.T) {
	_, inner, svc := newCachedForTest(t)
	ctx := context.Background()
	tenantA, tenantB, user := uuid.New(), uuid.New(), uuid.New()
	projA := inner.seed(tenantA, "secret-of-A")

	// Warm the cache under tenant A.
	if _, err := svc.Get(ctx, tenantA, user, projA.ID); err != nil {
		t.Fatalf("tenant A Get: %v", err)
	}

	// Tenant B asks for A's project ID. The cached entry is under A's
	// namespace, so the inner layer must be consulted and its error returned.
	before := inner.getCalls
	got, err := svc.Get(ctx, tenantB, user, projA.ID)
	if !errors.Is(err, errNotFound) {
		t.Fatalf("tenant B Get err = %v, want inner's not-found error", err)
	}
	if got.ID != uuid.Nil || got.Name != "" {
		t.Fatalf("tenant B received project data: %+v", got)
	}
	if inner.getCalls != before+1 {
		t.Fatalf("inner Get calls = %d, want %d (cache must not answer for tenant B)", inner.getCalls, before+1)
	}
}

func TestCachedCreateInvalidatesList(t *testing.T) {
	mr, inner, svc := newCachedForTest(t)
	ctx := context.Background()
	tenant, user := uuid.New(), uuid.New()
	inner.seed(tenant, "one")

	list, err := svc.List(ctx, tenant, user, 50)
	if err != nil || len(list) != 1 {
		t.Fatalf("first List: len=%d err=%v", len(list), err)
	}
	// Second List is a hit and does not reach the inner layer.
	if _, err := svc.List(ctx, tenant, user, 50); err != nil {
		t.Fatalf("second List: %v", err)
	}
	if inner.listCalls != 1 {
		t.Fatalf("inner List calls = %d after a hit, want 1", inner.listCalls)
	}
	// Pin the key format: tenantly:t:{tenant}:projects:list:{limit}.
	if !mr.Exists(cache.Key(tenant, "projects", "list", "50")) {
		t.Fatalf("list key %q not found in redis", cache.Key(tenant, "projects", "list", "50"))
	}

	if _, err := svc.Create(ctx, tenant, user, "two"); err != nil {
		t.Fatalf("Create: %v", err)
	}

	list, err = svc.List(ctx, tenant, user, 50)
	if err != nil {
		t.Fatalf("List after Create: %v", err)
	}
	if inner.listCalls != 2 {
		t.Fatalf("inner List calls = %d, want 2 (Create must invalidate the list cache)", inner.listCalls)
	}
	if len(list) != 2 {
		t.Fatalf("List after Create returned %d projects, want 2 (stale cache)", len(list))
	}
}

func TestCachedCreateDoesNotInvalidateOtherTenant(t *testing.T) {
	_, inner, svc := newCachedForTest(t)
	ctx := context.Background()
	tenantA, tenantB, user := uuid.New(), uuid.New(), uuid.New()
	inner.seed(tenantB, "b-one")

	if _, err := svc.List(ctx, tenantB, user, 50); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Create(ctx, tenantA, user, "a-new"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.List(ctx, tenantB, user, 50); err != nil {
		t.Fatal(err)
	}
	if inner.listCalls != 1 {
		t.Fatalf("tenant B list re-fetched (%d inner calls); tenant A's Create must not touch it", inner.listCalls)
	}
}
