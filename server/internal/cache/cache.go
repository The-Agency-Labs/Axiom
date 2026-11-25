// Package cache stores provider responses in SQLite (gzip JSON, with expiry)
// behind a small in-memory LRU.
package cache

import (
	"bytes"
	"compress/gzip"
	"container/list"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"

	"github.com/the-agency-labs/encompass/server/internal/db"
	"github.com/the-agency-labs/encompass/server/internal/jobs"
)

// DefaultEntries is the default LRU capacity.
const DefaultEntries = 256

// Cache is safe for concurrent use.
type Cache struct {
	q   *db.Queries
	now func() time.Time
	lru *lru
	sf  singleflight.Group
}

// New creates a Cache. entries <= 0 uses DefaultEntries.
func New(q *db.Queries, entries int) *Cache {
	if entries <= 0 {
		entries = DefaultEntries
	}
	return &Cache{q: q, now: func() time.Time { return time.Now().UTC() }, lru: newLRU(entries)}
}

// Get loads key into dst (JSON-decoded) and reports whether a fresh entry existed.
func (c *Cache) Get(ctx context.Context, key string, dst any) (bool, error) {
	data, ok, err := c.raw(ctx, key)
	if err != nil || !ok {
		return false, err
	}
	if err := json.Unmarshal(data, dst); err != nil {
		return false, fmt.Errorf("cache %q: %w", key, err)
	}
	return true, nil
}

// raw returns the JSON bytes for key, from memory or SQLite (then promoted to memory).
func (c *Cache) raw(ctx context.Context, key string) ([]byte, bool, error) {
	now := c.now()
	if data, ok := c.lru.get(key, now); ok {
		return data, true, nil
	}
	row, err := c.q.GetCache(ctx, db.GetCacheParams{Key: key, ExpiresAt: now.Format(time.RFC3339)})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	data, err := gunzip(row.Value)
	if err != nil {
		return nil, false, fmt.Errorf("cache %q: %w", key, err)
	}
	if exp, err := time.Parse(time.RFC3339, row.ExpiresAt); err == nil {
		c.lru.set(key, data, exp)
	}
	return data, true, nil
}

// Set stores value (JSON-encoded) under key for ttl, in memory and in SQLite.
func (c *Cache) Set(ctx context.Context, key string, value any, ttl time.Duration) error {
	data, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("cache %q: %w", key, err)
	}
	return c.setRaw(ctx, key, data, ttl)
}

func (c *Cache) setRaw(ctx context.Context, key string, data []byte, ttl time.Duration) error {
	zipped, err := gzipBytes(data)
	if err != nil {
		return err
	}
	exp := c.now().Add(ttl)
	if err := c.q.SetCache(ctx, db.SetCacheParams{Key: key, Value: zipped, ExpiresAt: exp.Format(time.RFC3339)}); err != nil {
		return err
	}
	c.lru.set(key, data, exp)
	return nil
}

// GetOrFetch fills dst from the cache, or calls fetch, caches its result for
// ttl and fills dst from that. Concurrent calls for the same key share one
// fetch. Errors from fetch are returned and not cached.
func (c *Cache) GetOrFetch(ctx context.Context, key string, ttl time.Duration, dst any, fetch func() (any, error)) error {
	if ok, err := c.Get(ctx, key, dst); err != nil {
		slog.WarnContext(ctx, "cache read failed, fetching", "key", key, "err", err)
	} else if ok {
		return nil
	}
	ch := c.sf.DoChan(key, func() (any, error) {
		// Another caller may have filled it while we waited to become the leader.
		if data, ok, err := c.raw(context.WithoutCancel(ctx), key); err == nil && ok {
			return data, nil
		}
		value, err := fetch()
		if err != nil {
			return nil, err
		}
		data, err := json.Marshal(value)
		if err != nil {
			return nil, fmt.Errorf("cache %q: %w", key, err)
		}
		if err := c.setRaw(context.WithoutCancel(ctx), key, data, ttl); err != nil {
			slog.WarnContext(ctx, "cache write failed", "key", key, "err", err)
		}
		return data, nil
	})
	select {
	case <-ctx.Done():
		return ctx.Err()
	case res := <-ch:
		if res.Err != nil {
			return res.Err
		}
		return json.Unmarshal(res.Val.([]byte), dst)
	}
}

// Cleanup deletes expired rows and returns how many.
func (c *Cache) Cleanup(ctx context.Context) (int64, error) {
	return c.q.DeleteExpiredCache(ctx, c.now().Format(time.RFC3339))
}

// RegisterJobs schedules the hourly cleanup of expired rows.
func (c *Cache) RegisterJobs(m *jobs.Manager) {
	m.Every("cache-cleanup", time.Hour, func(ctx context.Context) error {
		n, err := c.Cleanup(ctx)
		if err == nil && n > 0 {
			slog.Info("cache cleanup", "deleted", n)
		}
		return err
	})
}

func gzipBytes(data []byte) ([]byte, error) {
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if _, err := zw.Write(data); err != nil {
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func gunzip(data []byte) ([]byte, error) {
	zr, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	defer zr.Close()
	return io.ReadAll(zr)
}

// lru is a fixed-capacity least-recently-used map with per-entry expiry.
type lru struct {
	mu    sync.Mutex
	cap   int
	order *list.List // front = most recent
	items map[string]*list.Element
}

type lruEntry struct {
	key     string
	data    []byte
	expires time.Time
}

func newLRU(capacity int) *lru {
	return &lru{cap: capacity, order: list.New(), items: map[string]*list.Element{}}
}

func (l *lru) get(key string, now time.Time) ([]byte, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	el, ok := l.items[key]
	if !ok {
		return nil, false
	}
	e := el.Value.(*lruEntry)
	if !now.Before(e.expires) {
		l.order.Remove(el)
		delete(l.items, key)
		return nil, false
	}
	l.order.MoveToFront(el)
	return e.data, true
}

func (l *lru) set(key string, data []byte, expires time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if el, ok := l.items[key]; ok {
		e := el.Value.(*lruEntry)
		e.data, e.expires = data, expires
		l.order.MoveToFront(el)
		return
	}
	l.items[key] = l.order.PushFront(&lruEntry{key: key, data: data, expires: expires})
	for l.order.Len() > l.cap {
		oldest := l.order.Back()
		l.order.Remove(oldest)
		delete(l.items, oldest.Value.(*lruEntry).key)
	}
}
