package jobs

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/the-agency-labs/axiom/server/internal/db"
)

func newManager(t *testing.T) (*Manager, *db.Queries, *sql.DB) {
	t.Helper()
	conn, err := db.Open(context.Background(), filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	q := db.New(conn)
	m := New(conn, q, time.UTC)
	m.backoff = func(int) time.Duration { return 0 }
	m.startupDelay, m.startupStagger = 0, 0
	return m, q, conn
}

func start(t *testing.T, m *Manager) (stop func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); m.Run(ctx) }()
	return func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("Run did not stop")
		}
	}
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestEnqueueRunsHandler(t *testing.T) {
	m, q, _ := newManager(t)
	got := make(chan Job, 1)
	m.Handle("echo", func(ctx context.Context, j Job) (string, error) { got <- j; return "ok:" + string(j.Payload), nil })
	defer start(t, m)()

	id, err := m.Enqueue(context.Background(), "echo", 0, map[string]int{"n": 1})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case j := <-got:
		if j.ID != id || string(j.Payload) != `{"n":1}` || j.Attempts != 1 {
			t.Fatalf("job: %+v", j)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("handler not called")
	}
	waitFor(t, "done", func() bool { r, _ := q.GetJob(context.Background(), id); return r.Status == "done" })
	r, _ := q.GetJob(context.Background(), id)
	if r.Result != `ok:{"n":1}` || !r.FinishedAt.Valid {
		t.Fatalf("row: %+v", r)
	}
}

func TestRetryThenFail(t *testing.T) {
	m, q, _ := newManager(t)
	var calls atomic.Int32
	m.Handle("boom", func(ctx context.Context, j Job) (string, error) { calls.Add(1); return "", errors.New("nope") })
	defer start(t, m)()
	id, _ := m.Enqueue(context.Background(), "boom", 0, nil)
	waitFor(t, "failed", func() bool { r, _ := q.GetJob(context.Background(), id); return r.Status == "failed" })
	r, _ := q.GetJob(context.Background(), id)
	if calls.Load() != MaxAttempts || r.Attempts != MaxAttempts || r.Result != "nope" {
		t.Fatalf("calls=%d row=%+v", calls.Load(), r)
	}
}

func TestPanicAndMissingHandler(t *testing.T) {
	m, q, _ := newManager(t)
	var calls atomic.Int32
	m.Handle("panic", func(ctx context.Context, j Job) (string, error) {
		if calls.Add(1) == 1 {
			panic("kaboom")
		}
		return "recovered", nil
	})
	defer start(t, m)()
	id, _ := m.Enqueue(context.Background(), "panic", 0, nil)
	missing, _ := m.Enqueue(context.Background(), "unknown", 0, nil)
	waitFor(t, "both finished", func() bool {
		a, _ := q.GetJob(context.Background(), id)
		b, _ := q.GetJob(context.Background(), missing)
		return a.Status == "done" && b.Status == "failed"
	})
}

func TestCrashRecoveryRequeuesRunning(t *testing.T) {
	m, q, conn := newManager(t)
	ran := make(chan int64, 1)
	m.Handle("x", func(ctx context.Context, j Job) (string, error) { ran <- j.ID; return "", nil })
	id, _ := m.Enqueue(context.Background(), "x", 0, nil)
	// Simulate a crash mid-run.
	if _, err := conn.Exec("UPDATE jobs SET status='running', attempts=1 WHERE id=?", id); err != nil {
		t.Fatal(err)
	}
	defer start(t, m)()
	select {
	case got := <-ran:
		if got != id {
			t.Fatal("wrong job")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("interrupted job was not requeued")
	}
	_ = q
}

func TestFutureJobsWait(t *testing.T) {
	m, q, conn := newManager(t)
	var calls atomic.Int32
	m.Handle("later", func(ctx context.Context, j Job) (string, error) { calls.Add(1); return "", nil })
	id, _ := m.Enqueue(context.Background(), "later", 0, nil)
	conn.Exec("UPDATE jobs SET run_at='2999-01-01T00:00:00Z' WHERE id=?", id)
	m2, _, _ := m, q, id
	defer start(t, m2)()
	time.Sleep(200 * time.Millisecond)
	if calls.Load() != 0 {
		t.Fatal("ran before its time")
	}
}

func TestPeriodicNoOverlapPanicSafeAndRecorded(t *testing.T) {
	m, q, _ := newManager(t)
	var running, maxRunning, calls atomic.Int32
	m.Every("tick", 10*time.Millisecond, func(ctx context.Context) error {
		n := running.Add(1)
		if n > maxRunning.Load() {
			maxRunning.Store(n)
		}
		defer running.Add(-1)
		time.Sleep(25 * time.Millisecond) // longer than the interval
		if calls.Add(1) == 1 {
			panic("first run panics")
		}
		return nil
	})
	stop := start(t, m)
	waitFor(t, "three runs", func() bool { return calls.Load() >= 3 })
	stop()
	if maxRunning.Load() != 1 {
		t.Fatalf("overlapping runs: %d", maxRunning.Load())
	}
	v, err := q.GetKV(context.Background(), "job:tick:last")
	if err != nil || v == "" {
		t.Fatalf("kv: %q %v", v, err)
	}
}

func TestDailyNext(t *testing.T) {
	loc, err := time.LoadLocation("Europe/Amsterdam")
	if err != nil {
		t.Skip("no tzdata")
	}
	m, _, _ := newManager(t)
	m.loc = loc
	m.Daily("d", 4, 30, func(context.Context) error { return nil })
	next := m.periodic[0].next
	at := func(s string) time.Time { ts, _ := time.ParseInLocation("2006-01-02 15:04", s, loc); return ts }
	if got := next(at("2026-03-01 03:00")); !got.Equal(at("2026-03-01 04:30")) {
		t.Fatalf("before: %v", got)
	}
	if got := next(at("2026-03-01 04:30")); !got.Equal(at("2026-03-02 04:30")) {
		t.Fatalf("at: %v", got)
	}
	// Across the spring-forward change (2026-03-29) it stays at 04:30 local.
	if got := next(at("2026-03-28 05:00")); !got.Equal(at("2026-03-29 04:30")) {
		t.Fatalf("dst: %v", got)
	}
}

func TestFirstRunKeepsScheduleAcrossRestart(t *testing.T) {
	m, q, _ := newManager(t)
	m.startupDelay, m.startupStagger = 2*time.Minute, 30*time.Second
	now := time.Date(2026, 11, 25, 20, 0, 0, 0, time.UTC)
	m.now = func() time.Time { return now }
	m.Every("recent", 6*time.Hour, func(context.Context) error { return nil })
	m.Every("stale", 6*time.Hour, func(context.Context) error { return nil })
	m.Every("never", 30*time.Minute, func(context.Context) error { return nil })
	ctx := context.Background()
	set := func(name string, at time.Time) {
		b, _ := json.Marshal(lastRun{At: at.Format(time.RFC3339), OK: true})
		if err := q.SetKV(ctx, db.SetKVParams{Key: "job:" + name + ":last", Value: string(b), UpdatedAt: at.Format(time.RFC3339)}); err != nil {
			t.Fatal(err)
		}
	}
	set("recent", now.Add(-time.Hour))
	set("stale", now.Add(-7*time.Hour))

	at, caught := m.firstRun(ctx, m.periodic[0], 0)
	if caught || !at.Equal(now.Add(5*time.Hour)) {
		t.Fatalf("recent: %v %v, want schedule kept (5h)", at, caught)
	}
	at, caught = m.firstRun(ctx, m.periodic[1], 0)
	if !caught || !at.Equal(now.Add(2*time.Minute)) {
		t.Fatalf("stale: %v %v, want catch-up after 2m", at, caught)
	}
	at, caught = m.firstRun(ctx, m.periodic[2], 1)
	if !caught || !at.Equal(now.Add(2*time.Minute+30*time.Second)) {
		t.Fatalf("never: %v %v, want staggered catch-up", at, caught)
	}
}
