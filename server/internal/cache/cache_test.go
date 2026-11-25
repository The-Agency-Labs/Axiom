package cache

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/the-agency-labs/encompass/server/internal/db"
)

type payload struct {
	Name string `json:"name"`
	N    int    `json:"n"`
}

func newCache(t *testing.T, entries int) (*Cache, *db.Queries) {
	t.Helper()
	conn, err := db.Open(context.Background(), filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	q := db.New(conn)
	return New(q, entries), q
}

func TestSetGetPersistsAndExpires(t *testing.T) {
	c, q := newCache(t, 4)
	ctx := context.Background()
	if err := c.Set(ctx, "k", payload{"a", 1}, time.Hour); err != nil {
		t.Fatal(err)
	}
	var got payload
	if ok, err := c.Get(ctx, "k", &got); !ok || err != nil || got != (payload{"a", 1}) {
		t.Fatalf("get: %v %v %+v", ok, err, got)
	}
	// A second Cache over the same DB (cold LRU) reads from SQLite.
	cold := New(q, 4)
	got = payload{}
	if ok, err := cold.Get(ctx, "k", &got); !ok || err != nil || got != (payload{"a", 1}) {
		t.Fatalf("cold get: %v %v %+v", ok, err, got)
	}
	if ok, _ := c.Get(ctx, "missing", &got); ok {
		t.Fatal("missing key found")
	}
	// Expiry, in memory and in SQLite.
	later := time.Now().UTC().Add(2 * time.Hour)
	c.now = func() time.Time { return later }
	cold.now = c.now
	if ok, _ := c.Get(ctx, "k", &got); ok {
		t.Fatal("expired entry served from memory")
	}
	if ok, _ := cold.Get(ctx, "k", &got); ok {
		t.Fatal("expired entry served from sqlite")
	}
	if n, err := c.Cleanup(ctx); err != nil || n != 1 {
		t.Fatalf("cleanup n=%d err=%v", n, err)
	}
}

func TestStoredValueIsGzip(t *testing.T) {
	c, q := newCache(t, 4)
	ctx := context.Background()
	c.Set(ctx, "k", payload{"a", 1}, time.Hour)
	row, err := q.GetCache(ctx, db.GetCacheParams{Key: "k", ExpiresAt: "2000-01-01T00:00:00Z"})
	if err != nil || len(row.Value) < 2 || row.Value[0] != 0x1f || row.Value[1] != 0x8b {
		t.Fatalf("not gzip: %v %v", row.Value, err)
	}
}

func TestLRUEvictsLeastRecentlyUsed(t *testing.T) {
	l := newLRU(2)
	now := time.Now()
	exp := now.Add(time.Hour)
	l.set("a", []byte("1"), exp)
	l.set("b", []byte("2"), exp)
	l.get("a", now) // a is now most recent
	l.set("c", []byte("3"), exp)
	if _, ok := l.get("b", now); ok {
		t.Fatal("b should have been evicted")
	}
	if _, ok := l.get("a", now); !ok {
		t.Fatal("a evicted")
	}
	if _, ok := l.get("c", now); !ok {
		t.Fatal("c missing")
	}
	if l.order.Len() != 2 || len(l.items) != 2 {
		t.Fatalf("size %d/%d", l.order.Len(), len(l.items))
	}
}

func TestGetOrFetchDeduplicates(t *testing.T) {
	c, _ := newCache(t, 4)
	ctx := context.Background()
	var calls atomic.Int32
	release := make(chan struct{})
	fetch := func() (any, error) {
		calls.Add(1)
		<-release
		return payload{"x", 7}, nil
	}
	var wg sync.WaitGroup
	results := make([]payload, 10)
	for i := range results {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := c.GetOrFetch(ctx, "k", time.Hour, &results[i], fetch); err != nil {
				t.Error(err)
			}
		}()
	}
	time.Sleep(100 * time.Millisecond) // let all callers join the flight
	close(release)
	wg.Wait()
	if calls.Load() != 1 {
		t.Fatalf("fetch called %d times", calls.Load())
	}
	for _, r := range results {
		if r != (payload{"x", 7}) {
			t.Fatalf("result %+v", r)
		}
	}
	// Now cached: no more fetches.
	var again payload
	if err := c.GetOrFetch(ctx, "k", time.Hour, &again, fetch); err != nil || calls.Load() != 1 {
		t.Fatalf("cached call: %v calls=%d", err, calls.Load())
	}
}

func TestGetOrFetchErrorNotCached(t *testing.T) {
	c, _ := newCache(t, 4)
	ctx := context.Background()
	var dst payload
	boom := errors.New("upstream down")
	if err := c.GetOrFetch(ctx, "k", time.Hour, &dst, func() (any, error) { return nil, boom }); !errors.Is(err, boom) {
		t.Fatalf("err = %v", err)
	}
	if err := c.GetOrFetch(ctx, "k", time.Hour, &dst, func() (any, error) { return payload{"ok", 1}, nil }); err != nil || dst.Name != "ok" {
		t.Fatalf("retry: %v %+v", err, dst)
	}
}
