package cache

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

func newTestCache(t *testing.T, ttl time.Duration) (*miniredis.Miniredis, *Cache) {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr(), MaxRetries: -1})
	t.Cleanup(func() { _ = rdb.Close() })
	return mr, New(rdb, ttl)
}

type payload struct {
	Name string `json:"name"`
	N    int    `json:"n"`
}

func TestKeyFormat(t *testing.T) {
	id := uuid.MustParse("11111111-2222-3333-4444-555555555555")
	got := Key(id, "project", "abc")
	want := "tenantly:t:11111111-2222-3333-4444-555555555555:project:abc"
	if got != want {
		t.Fatalf("Key = %q, want %q", got, want)
	}
	if got := Key(id, "projects", "list", "50"); got != "tenantly:t:11111111-2222-3333-4444-555555555555:projects:list:50" {
		t.Fatalf("list key = %q", got)
	}
}

func TestSetGetRoundTrip(t *testing.T) {
	_, c := newTestCache(t, time.Minute)
	ctx := context.Background()
	key := Key(uuid.New(), "thing")

	var miss payload
	hit, err := c.GetJSON(ctx, key, &miss)
	if err != nil || hit {
		t.Fatalf("GetJSON before set: hit=%v err=%v, want miss with no error", hit, err)
	}

	if err := c.SetJSON(ctx, key, payload{Name: "x", N: 7}); err != nil {
		t.Fatalf("SetJSON: %v", err)
	}
	var got payload
	hit, err = c.GetJSON(ctx, key, &got)
	if err != nil || !hit {
		t.Fatalf("GetJSON after set: hit=%v err=%v", hit, err)
	}
	if got != (payload{Name: "x", N: 7}) {
		t.Fatalf("round trip = %+v", got)
	}
}

func TestSetUsesTTL(t *testing.T) {
	mr, c := newTestCache(t, 30*time.Second)
	ctx := context.Background()
	key := Key(uuid.New(), "ttl")
	if err := c.SetJSON(ctx, key, payload{N: 1}); err != nil {
		t.Fatalf("SetJSON: %v", err)
	}
	mr.FastForward(31 * time.Second)
	var got payload
	hit, err := c.GetJSON(ctx, key, &got)
	if err != nil || hit {
		t.Fatalf("after TTL: hit=%v err=%v, want expired miss", hit, err)
	}
}

func TestDeleteTenantPrefixLeavesOtherTenant(t *testing.T) {
	mr, c := newTestCache(t, time.Minute)
	ctx := context.Background()
	tenantA := uuid.New()
	tenantB := uuid.New()

	for i := 0; i < 5; i++ {
		k := Key(tenantA, "projects", "list", fmt.Sprint(i))
		if err := c.SetJSON(ctx, k, payload{N: i}); err != nil {
			t.Fatal(err)
		}
	}
	keepA := Key(tenantA, "project", "alpha")
	keepB := Key(tenantB, "projects", "list", "50")
	for _, k := range []string{keepB, keepA} {
		if err := c.SetJSON(ctx, k, payload{N: 99}); err != nil {
			t.Fatal(err)
		}
	}

	if err := c.DeleteTenantPrefix(ctx, tenantA, "projects:list"); err != nil {
		t.Fatalf("DeleteTenantPrefix: %v", err)
	}

	for i := 0; i < 5; i++ {
		if mr.Exists(Key(tenantA, "projects", "list", fmt.Sprint(i))) {
			t.Fatalf("tenant A list key %d still present", i)
		}
	}
	if !mr.Exists(keepB) {
		t.Fatal("tenant B list key was deleted by tenant A's invalidation")
	}
	if !mr.Exists(keepA) {
		t.Fatal("tenant A single-project key was deleted by a projects:list prefix")
	}
}

func TestDeleteTenantPrefixTreatsGlobCharsLiterally(t *testing.T) {
	mr, c := newTestCache(t, time.Minute)
	ctx := context.Background()
	tenant := uuid.New()

	// "*" in a prefix must not act as a wildcard over the tenant's other keys.
	if err := c.SetJSON(ctx, Key(tenant, "a*b", "1"), payload{}); err != nil {
		t.Fatal(err)
	}
	if err := c.SetJSON(ctx, Key(tenant, "axyb", "1"), payload{}); err != nil {
		t.Fatal(err)
	}
	if err := c.DeleteTenantPrefix(ctx, tenant, "a*b"); err != nil {
		t.Fatal(err)
	}
	if mr.Exists(Key(tenant, "a*b", "1")) {
		t.Fatal("literal a*b key not deleted")
	}
	if !mr.Exists(Key(tenant, "axyb", "1")) {
		t.Fatal("key axyb was deleted; glob character was not escaped")
	}
}
