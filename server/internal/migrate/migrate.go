// Package migrate is the one-shot import of an old Django Axiom
// SQLite database into the Go schema.
//
// What is copied and what is refetched:
//
//   - copied: users (password hashes untouched, so logins keep working and
//     are rehashed on first login), API tokens (hashes and md5 untouched, so
//     KOSync devices and assistants keep working), the Django user token as
//     both the Jellyfin webhook and the calendar token, per-type settings,
//     items and their ids, every entry with its seasons, plays and history,
//     reading documents/page events/sessions, people, credits, lists.
//   - refetched: item metadata (year, synopsis, totals, genres: Django kept
//     only title and image), TMDB seasons and episodes, releases (the
//     calendar), and the anime mapping (downloaded from Fribb; the Django
//     copy is used only when the download fails). Django's release events
//     are not copied: the release refresh job rebuilds them.
//
// Run never touches the Django database (it is opened read-only).
package migrate

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/the-agency-labs/axiom/server/internal/catalog/mappings"
	"github.com/the-agency-labs/axiom/server/internal/db"
)

// ErrTargetNotEmpty is returned when the target already holds entries and
// Force is not set.
var ErrTargetNotEmpty = errors.New("migrate: target database already has entries (use --force to replace its data)")

// ErrSkip is what a RefreshItem or SyncSeasons hook returns for an item it
// deliberately leaves alone (no provider key): counted, not a warning.
var ErrSkip = errors.New("migrate: skipped")

// Options configure a migration.
type Options struct {
	// Source is the Django database (opened read-only by the caller).
	Source *sql.DB
	// Target is the Go database, already migrated (db.Open does that).
	Target *sql.DB

	// Timezone is stored on every user (Django kept the zone in the TZ env,
	// not on the user). Default UTC.
	Timezone string
	// DisableKinds are media kinds switched off for every user whatever the
	// Django flags say (the owner has comics off).
	DisableKinds []string

	DryRun bool
	// Force replaces whatever data the target holds.
	Force bool

	// LoadMappings downloads the Fribb anime mapping into the store. Optional;
	// when it is missing or fails, the Django copy of the table is used.
	LoadMappings func(ctx context.Context) error
	// RefreshItem refetches an item's metadata and ids; SyncSeasons fetches a
	// show's TMDB seasons and episodes. Both are optional (nil skips them).
	RefreshItem func(ctx context.Context, itemID int64) error
	SyncSeasons func(ctx context.Context, itemID int64) error
	// AllHistory also copies history rows no user made (bulk imports).
	AllHistory bool
	// SkipAssumed leaves out the assumed-earlier-seasons step.
	SkipAssumed bool

	// Now is the clock (tests); Loc is the zone "today" is read in.
	Now func() time.Time
	Loc *time.Location
	Log *slog.Logger
}

// Report says what a run did.
type Report struct {
	DryRun   bool
	Counts   map[string]int
	Warnings []string
	// Assumed lists the shows the assumed-earlier-seasons rule touched (or,
	// in a dry run, would touch), and Deferred those it could not apply for
	// lack of episode data (no TMDB access).
	Assumed  []AssumedShow
	Deferred []AssumedShow
}

func (r *Report) count(k string, n int) { r.Counts[k] += n }

const maxWarnings = 100

func (r *Report) warn(format string, args ...any) {
	if len(r.Warnings) < maxWarnings {
		r.Warnings = append(r.Warnings, fmt.Sprintf(format, args...))
	} else if len(r.Warnings) == maxWarnings {
		r.Warnings = append(r.Warnings, "more warnings omitted")
	}
}

// Summary renders the report as plain text (counts and titles only).
func (r *Report) Summary() string {
	var b strings.Builder
	if r.DryRun {
		b.WriteString("DRY RUN: nothing was written\n")
	}
	keys := make([]string, 0, len(r.Counts))
	for k := range r.Counts {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Fprintf(&b, "%-28s %d\n", k, r.Counts[k])
	}
	if len(r.Assumed) > 0 {
		fmt.Fprintf(&b, "assumed earlier seasons (%d shows):\n", len(r.Assumed))
		for _, a := range r.Assumed {
			fmt.Fprintf(&b, "  %s [%s]: first watched season %d, assumed seasons %v, %d episodes\n", a.Title, a.Kind, a.FirstSeason, a.Seasons, a.Episodes)
		}
	}
	if len(r.Deferred) > 0 {
		fmt.Fprintf(&b, "assumed seasons NOT applied, no episode data (%d shows; rerun with TMDB access: axiom migrate-django --assumed-only):\n", len(r.Deferred))
		for _, a := range r.Deferred {
			fmt.Fprintf(&b, "  %s [%s]: first watched season %d, would assume seasons %v\n", a.Title, a.Kind, a.FirstSeason, a.Seasons)
		}
	}
	for _, w := range r.Warnings {
		fmt.Fprintf(&b, "warning: %s\n", w)
	}
	return b.String()
}

type migrator struct {
	opts Options
	ctx  context.Context
	tx   *sql.Tx
	rep  *Report
	log  *slog.Logger

	userMap map[int64]int64 // Django user id -> user id
	itemMap map[int64]int64 // Django item id -> item id (shows, films, books, ...)
	// showOf maps a Django season/episode item to the id of its show item.
	showOf map[int64]int64
	// itemKind remembers the Go kind of each migrated item.
	itemKind map[int64]string
	// entryBy maps "<django table>:<django id>" to the entry it became.
	entryBy map[string]int64

	animeShows, animeFilms map[string]struct{}
}

func (o *Options) defaults() {
	if o.Timezone == "" {
		o.Timezone = "UTC"
	}
	if o.Now == nil {
		o.Now = func() time.Time { return time.Now().UTC() }
	}
	if o.Loc == nil {
		o.Loc = time.UTC
	}
	if o.Log == nil {
		o.Log = slog.Default()
	}
}

// Run migrates the Django database into the target.
func Run(ctx context.Context, opts Options) (*Report, error) {
	opts.defaults()
	rep := &Report{DryRun: opts.DryRun, Counts: map[string]int{}}
	if opts.Source == nil || opts.Target == nil {
		return nil, errors.New("migrate: source and target are required")
	}
	if !hasTable(ctx, opts.Source, "app_item") || !hasTable(ctx, opts.Source, "users_user") {
		return nil, errors.New("migrate: source is not an old Django Axiom database")
	}
	var existing int
	if err := opts.Target.QueryRowContext(ctx, `SELECT COUNT(*) FROM entries`).Scan(&existing); err != nil {
		return nil, err
	}
	if existing > 0 && !opts.Force {
		return nil, ErrTargetNotEmpty
	}

	store := mappings.New(opts.Target, db.New(opts.Target), nil)
	if err := ensureMappings(ctx, &opts, rep); err != nil {
		return nil, err
	}
	ids, err := store.AnimeTMDBIDs(ctx)
	if err != nil {
		return nil, err
	}

	tx, err := opts.Target.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback() //nolint:errcheck // no-op after commit
	m := &migrator{
		opts: opts, ctx: ctx, tx: tx, rep: rep, log: opts.Log,
		userMap: map[int64]int64{}, itemMap: map[int64]int64{}, showOf: map[int64]int64{},
		itemKind: map[int64]string{}, entryBy: map[string]int64{},
		animeShows: ids.Shows, animeFilms: ids.Films,
	}
	if opts.Force {
		if err := m.wipe(); err != nil {
			return nil, err
		}
	}
	steps := []struct {
		name string
		fn   func() error
	}{
		{"users", m.users},
		{"api tokens", m.apiTokens},
		{"items", m.items},
		{"tracking", m.tracking},
		{"history", m.history},
		{"reading", m.reading},
		{"people and credits", m.catalog},
	}
	for _, s := range steps {
		if err := s.fn(); err != nil {
			return nil, fmt.Errorf("migrate %s: %w", s.name, err)
		}
	}
	if opts.DryRun {
		if !opts.SkipAssumed {
			cands, err := assumedCandidates(ctx, tx)
			if err != nil {
				return nil, err
			}
			rep.Deferred = cands
		}
		return rep, nil // the deferred rollback discards everything
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}

	m.enrich()
	if !opts.SkipAssumed {
		ar, err := ApplyAssumed(ctx, opts)
		if err != nil {
			return rep, fmt.Errorf("migrate assumed seasons: %w", err)
		}
		rep.Assumed, rep.Deferred = ar.Assumed, ar.Deferred
		rep.count("assumed episodes", ar.Episodes)
	}
	return rep, nil
}

// wipe removes the data a previous run (or createuser) left behind.
func (m *migrator) wipe() error {
	for _, stmt := range []string{
		`DELETE FROM users`, // cascades entries, tokens, settings, documents, sessions, lists
		`DELETE FROM items`,
		`DELETE FROM people`,
	} {
		if _, err := m.tx.ExecContext(m.ctx, stmt); err != nil {
			return err
		}
	}
	return nil
}

// ensureMappings makes sure the anime mapping table is filled: the Fribb
// download when a loader is given, otherwise (or when it fails) the copy
// Django kept in catalog_animemapping.
func ensureMappings(ctx context.Context, opts *Options, rep *Report) error {
	var have int
	if err := opts.Target.QueryRowContext(ctx, `SELECT COUNT(*) FROM anime_mappings`).Scan(&have); err != nil {
		return err
	}
	if have > 0 && (opts.LoadMappings == nil || !opts.Force) {
		rep.count("anime mappings (kept)", have)
		return nil
	}
	if opts.LoadMappings != nil {
		err := opts.LoadMappings(ctx)
		if err == nil {
			_ = opts.Target.QueryRowContext(ctx, `SELECT COUNT(*) FROM anime_mappings`).Scan(&have)
			rep.count("anime mappings (downloaded)", have)
			return nil
		}
		rep.warn("anime mapping download failed (%v); using the copy from the Django database", err)
	}
	if !hasTable(ctx, opts.Source, "catalog_animemapping") {
		rep.warn("no anime mapping available: every TMDB title is imported as tv/movie")
		return nil
	}
	rows, err := queryRows(ctx, opts.Source, `SELECT * FROM catalog_animemapping ORDER BY id`)
	if err != nil {
		return err
	}
	tx, err := opts.Target.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck
	if _, err := tx.ExecContext(ctx, `DELETE FROM anime_mappings`); err != nil {
		return err
	}
	for _, r := range rows {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO anime_mappings (mal_id, anilist_id, kind, tmdb_tv, tmdb_movie, tmdb_season, tmdb_offset, tvdb_id)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			r.nullInt("mal_id"), r.nullInt("anilist_id"), r.str("kind"), r.nullInt("tmdb_tv"), r.nullInt("tmdb_movie"),
			r.nullInt("tmdb_season"), r.i64("tmdb_offset"), r.nullInt("tvdb_id")); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	rep.count("anime mappings (django copy)", len(rows))
	return nil
}

// enrich refetches metadata and seasons through the optional hooks. A failure
// of one item is a warning, never fatal: the data is refetchable later.
func (m *migrator) enrich() {
	if m.opts.RefreshItem == nil && m.opts.SyncSeasons == nil {
		return
	}
	rows, err := m.opts.Target.QueryContext(m.ctx, `SELECT id, kind, source FROM items ORDER BY id`)
	if err != nil {
		m.rep.warn("enrich: %v", err)
		return
	}
	type it struct {
		id           int64
		kind, source string
	}
	var items []it
	for rows.Next() {
		var i it
		if err := rows.Scan(&i.id, &i.kind, &i.source); err == nil {
			items = append(items, i)
		}
	}
	rows.Close()
	failed := 0
	for _, i := range items {
		if m.opts.RefreshItem != nil {
			err := m.opts.RefreshItem(m.ctx, i.id)
			switch {
			case errors.Is(err, ErrSkip):
				m.rep.count("items not refreshed (no provider key)", 1)
			case err != nil:
				failed++
				m.rep.warn("refresh item %d: %v", i.id, err)
				continue
			default:
				m.rep.count("items refreshed", 1)
			}
		}
		if m.opts.SyncSeasons != nil && (i.kind == "tv" || i.kind == "anime") && i.source == "tmdb" {
			err := m.opts.SyncSeasons(m.ctx, i.id)
			switch {
			case errors.Is(err, ErrSkip):
				m.rep.count("shows without seasons (no TMDB key)", 1)
			case err != nil:
				failed++
				m.rep.warn("sync seasons of item %d: %v", i.id, err)
			default:
				m.rep.count("shows with seasons synced", 1)
			}
		}
	}
	if failed > 0 {
		m.rep.count("enrich failures", failed)
	}
}

func (m *migrator) exec(query string, args ...any) (sql.Result, error) {
	return m.tx.ExecContext(m.ctx, query, args...)
}

func (m *migrator) insertID(query string, args ...any) (int64, error) {
	res, err := m.tx.ExecContext(m.ctx, query, args...)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}
