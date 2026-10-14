package service

import (
	"context"
	"log"
	"strconv"

	"github.com/google/uuid"

	"github.com/iboondk/tenantly/internal/cache"
	"github.com/iboondk/tenantly/internal/store"
)

// projectsListPrefix is the key prefix of every cached project list. Create
// deletes all keys under it.
const projectsListPrefix = "projects:list"

// cachedProjects reads through a tenant-namespaced cache in front of inner.
// Cache failures are logged, and the request falls through to inner.
//
// The tenantID used for keys must be the path tenant that already passed
// RequireMembership. The cache trusts that check.
type cachedProjects struct {
	inner Projects
	c     *cache.Cache
}

// NewCachedProjects wraps inner with a read-through cache. Get and List are
// cached. Create deletes the tenant's cached project lists.
func NewCachedProjects(inner Projects, c *cache.Cache) Projects {
	return &cachedProjects{inner: inner, c: c}
}

func (p *cachedProjects) Get(ctx context.Context, tenantID, userID, projectID uuid.UUID) (store.Project, error) {
	key := cache.Key(tenantID, "project", projectID.String())

	var cached store.Project
	hit, err := p.c.GetJSON(ctx, key, &cached)
	if err != nil {
		log.Printf("service: cache get %s: %v", key, err)
	}
	if hit {
		return cached, nil
	}

	proj, err := p.inner.Get(ctx, tenantID, userID, projectID)
	if err != nil {
		return store.Project{}, err
	}
	if err := p.c.SetJSON(ctx, key, proj); err != nil {
		log.Printf("service: cache set %s: %v", key, err)
	}
	return proj, nil
}

func (p *cachedProjects) List(ctx context.Context, tenantID, userID uuid.UUID, limit int) ([]store.Project, error) {
	key := cache.Key(tenantID, "projects", "list", strconv.Itoa(limit))

	var cached []store.Project
	hit, err := p.c.GetJSON(ctx, key, &cached)
	if err != nil {
		log.Printf("service: cache get %s: %v", key, err)
	}
	if hit {
		return cached, nil
	}

	list, err := p.inner.List(ctx, tenantID, userID, limit)
	if err != nil {
		return nil, err
	}
	if err := p.c.SetJSON(ctx, key, list); err != nil {
		log.Printf("service: cache set %s: %v", key, err)
	}
	return list, nil
}

func (p *cachedProjects) Create(ctx context.Context, tenantID, userID uuid.UUID, name string) (store.Project, error) {
	proj, err := p.inner.Create(ctx, tenantID, userID, name)
	if err != nil {
		return store.Project{}, err
	}
	if err := p.c.DeleteTenantPrefix(ctx, tenantID, projectsListPrefix); err != nil {
		log.Printf("service: cache invalidate tenant %s: %v", tenantID, err)
	}
	return proj, nil
}
