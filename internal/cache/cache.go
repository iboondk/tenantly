// Package cache is a tenant-namespaced JSON cache on top of Redis.
//
// Every key starts with tenantly:t:{tenantID}: so one tenant's entries can
// never be read or deleted under another tenant's namespace.
package cache

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

const (
	keyRoot   = "tenantly:t:"
	scanCount = 200
)

// Cache stores JSON values with a fixed TTL.
type Cache struct {
	rdb redis.UniversalClient
	ttl time.Duration
}

// New returns a Cache that writes entries with ttl.
func New(rdb redis.UniversalClient, ttl time.Duration) *Cache {
	return &Cache{rdb: rdb, ttl: ttl}
}

// Key builds a tenant-namespaced key: tenantly:t:{tenantID}:part1:part2...
func Key(tenantID uuid.UUID, parts ...string) string {
	return keyRoot + tenantID.String() + ":" + strings.Join(parts, ":")
}

// GetJSON loads key into dst. hit is false when the key is missing. A
// decode failure returns hit false with an error.
func (c *Cache) GetJSON(ctx context.Context, key string, dst any) (bool, error) {
	raw, err := c.rdb.Get(ctx, key).Bytes()
	if errors.Is(err, redis.Nil) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("cache: get: %w", err)
	}
	if err := json.Unmarshal(raw, dst); err != nil {
		return false, fmt.Errorf("cache: decode: %w", err)
	}
	return true, nil
}

// SetJSON stores v under key with the cache TTL.
func (c *Cache) SetJSON(ctx context.Context, key string, v any) error {
	raw, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("cache: encode: %w", err)
	}
	if err := c.rdb.Set(ctx, key, raw, c.ttl).Err(); err != nil {
		return fmt.Errorf("cache: set: %w", err)
	}
	return nil
}

// DeleteTenantPrefix deletes every key in the tenant's namespace that starts
// with prefix. It uses SCAN, never KEYS, so it does not block Redis. Each key
// is removed through a pipeline, which also works on Redis Cluster.
func (c *Cache) DeleteTenantPrefix(ctx context.Context, tenantID uuid.UUID, prefix string) error {
	match := escapeGlob(Key(tenantID, prefix)) + "*"

	nodes := []redis.UniversalClient{c.rdb}
	if cl, ok := c.rdb.(*redis.ClusterClient); ok {
		nodes = nil
		err := cl.ForEachMaster(ctx, func(ctx context.Context, m *redis.Client) error {
			nodes = append(nodes, m)
			return nil
		})
		if err != nil {
			return fmt.Errorf("cache: cluster masters: %w", err)
		}
	}

	for _, node := range nodes {
		if err := scanDelete(ctx, node, match); err != nil {
			return err
		}
	}
	return nil
}

func scanDelete(ctx context.Context, node redis.UniversalClient, match string) error {
	var cursor uint64
	for {
		keys, next, err := node.Scan(ctx, cursor, match, scanCount).Result()
		if err != nil {
			return fmt.Errorf("cache: scan: %w", err)
		}
		if len(keys) > 0 {
			pipe := node.Pipeline()
			for _, k := range keys {
				pipe.Del(ctx, k)
			}
			if _, err := pipe.Exec(ctx); err != nil {
				return fmt.Errorf("cache: delete: %w", err)
			}
		}
		cursor = next
		if cursor == 0 {
			return nil
		}
	}
}

// escapeGlob backslash-escapes the characters that have a special meaning in
// a Redis glob pattern, so a key is matched literally.
func escapeGlob(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch r {
		case '*', '?', '[', ']', '\\':
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
}
