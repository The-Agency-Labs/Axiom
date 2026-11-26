// Package jobs runs periodic jobs registered in code and durable one-off jobs
// stored in the jobs table.
package jobs

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"runtime/debug"
	"sync"
	"time"

	"github.com/the-agency-labs/axiom/server/internal/db"
)

const (
	// MaxAttempts is how many times a failing one-off job runs before it is marked failed.
	MaxAttempts = 3
	// DefaultWorkers is the default size of the one-off worker pool.
	DefaultWorkers = 2

	pollInterval = 5 * time.Second

	// StartupDelay is how long after start a periodic job that is due (or has
	// never run) waits before its catch-up run, so the server is up first.
	StartupDelay = 2 * time.Minute
	// StartupStagger spaces those catch-up runs so they don't all start at once.
	StartupStagger = 30 * time.Second
)

// Job is a claimed one-off job handed to a handler.
type Job struct {
	ID       int64
	Kind     string
	UserID   int64 // 0 when the job has no user
	Payload  json.RawMessage
	Attempts int
}

// Handler runs a one-off job; the returned text is stored as the result.
type Handler func(ctx context.Context, job Job) (string, error)

type periodic struct {
	name string
	next func(now time.Time) time.Time
	fn   func(ctx context.Context) error
}

// Manager schedules periodic jobs and runs the durable queue.
type Manager struct {
	conn    *sql.DB
	q       *db.Queries
	loc     *time.Location
	workers int
	now     func() time.Time
	backoff func(attempt int) time.Duration

	mu       sync.Mutex
	periodic []periodic
	handlers map[string]Handler
	wake     chan struct{}
	started  bool

	startupDelay, startupStagger time.Duration
}

// New creates a Manager. loc is the zone Daily jobs use (nil = UTC).
func New(conn *sql.DB, q *db.Queries, loc *time.Location) *Manager {
	if loc == nil {
		loc = time.UTC
	}
	return &Manager{
		conn: conn, q: q, loc: loc, workers: DefaultWorkers,
		now:      func() time.Time { return time.Now().UTC() },
		backoff:  func(attempt int) time.Duration { return time.Duration(attempt*attempt) * time.Minute },
		handlers: map[string]Handler{},
		wake:     make(chan struct{}, 1),

		startupDelay: StartupDelay, startupStagger: StartupStagger,
	}
}

// SetWorkers sets the worker pool size; call before Run.
func (m *Manager) SetWorkers(n int) {
	if n > 0 {
		m.workers = n
	}
}

// Every registers a job that runs each interval. After a restart it keeps its
// schedule from the last recorded run; one that is overdue or has never run
// catches up shortly after start (see firstRun).
func (m *Manager) Every(name string, interval time.Duration, fn func(ctx context.Context) error) {
	m.addPeriodic(periodic{name: name, fn: fn, next: func(now time.Time) time.Time { return now.Add(interval) }})
}

// Daily registers a job that runs every day at hour:minute in the configured zone.
func (m *Manager) Daily(name string, hour, minute int, fn func(ctx context.Context) error) {
	m.addPeriodic(periodic{name: name, fn: fn, next: func(now time.Time) time.Time {
		local := now.In(m.loc)
		t := time.Date(local.Year(), local.Month(), local.Day(), hour, minute, 0, 0, m.loc)
		if !t.After(now) {
			t = time.Date(local.Year(), local.Month(), local.Day()+1, hour, minute, 0, 0, m.loc)
		}
		return t
	}})
}

func (m *Manager) addPeriodic(p periodic) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.started {
		panic("jobs: register jobs before Run")
	}
	m.periodic = append(m.periodic, p)
}

// Handle registers the handler for one-off jobs of kind.
func (m *Manager) Handle(kind string, fn Handler) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.started {
		panic("jobs: register handlers before Run")
	}
	m.handlers[kind] = fn
}

// Enqueue stores a one-off job to run as soon as a worker is free. payload may
// be nil, a string/[]byte/json.RawMessage of JSON, or any value to marshal.
// userID 0 means no user.
func (m *Manager) Enqueue(ctx context.Context, kind string, userID int64, payload any) (int64, error) {
	var body string
	switch v := payload.(type) {
	case nil:
		body = "{}"
	case string:
		body = v
	case []byte:
		body = string(v)
	case json.RawMessage:
		body = string(v)
	default:
		b, err := json.Marshal(v)
		if err != nil {
			return 0, fmt.Errorf("enqueue %s: %w", kind, err)
		}
		body = string(b)
	}
	id, err := m.q.EnqueueJob(ctx, db.EnqueueJobParams{
		Kind: kind, UserID: sql.NullInt64{Int64: userID, Valid: userID != 0},
		Payload: body, RunAt: m.now().Format(time.RFC3339),
	})
	if err != nil {
		return 0, err
	}
	select {
	case m.wake <- struct{}{}:
	default:
	}
	return id, nil
}

// Run starts the periodic jobs and the worker pool and blocks until ctx is
// cancelled and everything has stopped. Jobs left running by a crash are requeued first.
func (m *Manager) Run(ctx context.Context) error {
	m.mu.Lock()
	m.started = true
	m.mu.Unlock()

	if n, err := m.q.RequeueRunningJobs(ctx); err != nil {
		return fmt.Errorf("requeue running jobs: %w", err)
	} else if n > 0 {
		slog.Warn("requeued jobs interrupted by a crash", "count", n)
	}

	var wg sync.WaitGroup
	overdue := 0
	for _, p := range m.periodic {
		first, caughtUp := m.firstRun(ctx, p, overdue)
		if caughtUp {
			overdue++
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			m.runPeriodic(ctx, p, first)
		}()
	}
	for i := 0; i < m.workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			m.worker(ctx)
		}()
	}
	wg.Wait()
	return nil
}

// firstRun picks when a periodic job first runs after start: on its normal
// schedule counted from the last recorded run, or, when that time has passed
// or the job has never run, after the startup delay (the n-th overdue job
// staggered by n steps). caughtUp reports the second case.
func (m *Manager) firstRun(ctx context.Context, p periodic, n int) (at time.Time, caughtUp bool) {
	now := m.now()
	if v, err := m.q.GetKV(ctx, "job:"+p.name+":last"); err == nil {
		var last lastRun
		if json.Unmarshal([]byte(v), &last) == nil {
			if t, err := time.Parse(time.RFC3339, last.At); err == nil {
				if due := p.next(t); due.After(now) {
					return due, false
				}
			}
		}
	}
	return now.Add(m.startupDelay + time.Duration(n)*m.startupStagger), true
}

func (m *Manager) runPeriodic(ctx context.Context, p periodic, first time.Time) {
	for at := first; ; at = p.next(m.now()) {
		timer := time.NewTimer(at.Sub(m.now()))
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		m.runOnce(ctx, p)
	}
}

type lastRun struct {
	At         string `json:"at"`
	DurationMS int64  `json:"duration_ms"`
	OK         bool   `json:"ok"`
	Error      string `json:"error,omitempty"`
}

// runOnce runs a periodic job synchronously (so it can never overlap itself),
// recovering panics and recording the outcome in kv.
func (m *Manager) runOnce(ctx context.Context, p periodic) {
	start := m.now()
	err := safely(func() error { return p.fn(ctx) })
	dur := m.now().Sub(start)
	run := lastRun{At: start.Format(time.RFC3339), DurationMS: dur.Milliseconds(), OK: err == nil}
	if err != nil {
		run.Error = err.Error()
		slog.Error("job failed", "job", p.name, "duration", dur, "err", err)
	} else {
		slog.Info("job done", "job", p.name, "duration", dur)
	}
	b, _ := json.Marshal(run)
	// Use a fresh context so the result is still recorded during shutdown.
	wctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if err := m.q.SetKV(wctx, db.SetKVParams{Key: "job:" + p.name + ":last", Value: string(b), UpdatedAt: m.now().Format(time.RFC3339)}); err != nil {
		slog.Error("record job result", "job", p.name, "err", err)
	}
}

func safely(fn func() error) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("panic: %v\n%s", r, debug.Stack())
		}
	}()
	return fn()
}

func (m *Manager) worker(ctx context.Context) {
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	for {
		// Drain everything that is due, then sleep until woken.
		for ctx.Err() == nil && m.runNext(ctx) {
		}
		select {
		case <-ctx.Done():
			return
		case <-m.wake:
		case <-ticker.C:
		}
	}
}

// runNext claims and runs one due job; it reports whether it found one.
func (m *Manager) runNext(ctx context.Context) bool {
	now := m.now().Format(time.RFC3339)
	row, err := m.q.ClaimJob(ctx, sql.NullString{String: now, Valid: true})
	if errors.Is(err, sql.ErrNoRows) {
		return false
	}
	if err != nil {
		if ctx.Err() == nil {
			slog.Error("claim job", "err", err)
		}
		return false
	}
	job := Job{ID: row.ID, Kind: row.Kind, UserID: row.UserID.Int64, Payload: json.RawMessage(row.Payload), Attempts: int(row.Attempts)}

	m.mu.Lock()
	h := m.handlers[row.Kind]
	m.mu.Unlock()

	var result string
	if h == nil {
		err = fmt.Errorf("no handler for job kind %q", row.Kind)
		job.Attempts = MaxAttempts // retrying can't help
	} else {
		err = safely(func() (e error) { result, e = h(ctx, job); return })
	}

	// Record the outcome even when shutdown cancelled ctx.
	wctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	finished := m.now().Format(time.RFC3339)
	switch {
	case err == nil:
		slog.Info("job done", "kind", job.Kind, "id", job.ID)
		err = m.q.FinishJob(wctx, db.FinishJobParams{FinishedAt: sql.NullString{String: finished, Valid: true}, Result: result, ID: job.ID})
	case job.Attempts >= MaxAttempts:
		slog.Error("job failed", "kind", job.Kind, "id", job.ID, "attempts", job.Attempts, "err", err)
		err = m.q.FailJob(wctx, db.FailJobParams{FinishedAt: sql.NullString{String: finished, Valid: true}, Result: err.Error(), ID: job.ID})
	default:
		slog.Warn("job failed, will retry", "kind", job.Kind, "id", job.ID, "attempts", job.Attempts, "err", err)
		err = m.q.RetryJob(wctx, db.RetryJobParams{RunAt: m.now().Add(m.backoff(job.Attempts)).Format(time.RFC3339), Result: err.Error(), ID: job.ID})
	}
	if err != nil {
		slog.Error("record job outcome", "id", job.ID, "err", err)
	}
	return true
}
