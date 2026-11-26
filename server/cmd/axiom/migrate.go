package main

import (
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/the-agency-labs/axiom/server/internal/cache"
	"github.com/the-agency-labs/axiom/server/internal/catalog"
	"github.com/the-agency-labs/axiom/server/internal/catalog/mappings"
	"github.com/the-agency-labs/axiom/server/internal/config"
	"github.com/the-agency-labs/axiom/server/internal/db"
	"github.com/the-agency-labs/axiom/server/internal/jobs"
	"github.com/the-agency-labs/axiom/server/internal/migrate"
	"github.com/the-agency-labs/axiom/server/internal/providers"
)

// migrateDjango implements `axiom migrate-django`: the one-shot import of
// a Django database into the Go database at DATABASE_PATH.
func migrateDjango(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("migrate-django", flag.ContinueOnError)
	from := fs.String("from", "", "path of the Django SQLite database (read only)")
	dry := fs.Bool("dry-run", false, "read everything and report, write nothing")
	force := fs.Bool("force", false, "replace the data of a target that already has entries")
	tz := fs.String("timezone", "", "timezone stored on every user (default: TZ, else UTC)")
	disable := fs.String("disable-kinds", "comic", "comma separated media kinds switched off for every user")
	mapFile := fs.String("mappings-file", "", "Fribb anime-list JSON to load instead of downloading it")
	noRefresh := fs.Bool("no-refresh", false, "do not refetch item metadata and TMDB seasons")
	allHistory := fs.Bool("all-history", false, "also copy history rows no user made (bulk imports stamped with the import time)")
	skipAssumed := fs.Bool("skip-assumed", false, "skip the assumed-earlier-seasons step")
	assumedOnly := fs.Bool("assumed-only", false, "only apply the assumed-earlier-seasons step to an already migrated database")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("unexpected argument %q", fs.Arg(0))
	}
	if *from == "" && !*assumedOnly {
		return errors.New("usage: axiom migrate-django --from <django.db> [--dry-run] [--force]")
	}

	cfg, err := config.Load()
	if err != nil {
		return err
	}
	zone := *tz
	if zone == "" {
		zone = cfg.TimeZone
	}
	loc, err := time.LoadLocation(zone)
	if err != nil {
		return fmt.Errorf("timezone %q: %w", zone, err)
	}
	ctx := context.Background()
	conn, err := db.Open(ctx, cfg.DatabasePath)
	if err != nil {
		return err
	}
	defer conn.Close()
	queries := db.New(conn)
	providerCache := cache.New(queries, cache.DefaultEntries)
	cat := newCatalog(cfg, conn, queries, providerCache, jobs.New(conn, queries, loc))
	cat.Loc = loc

	opts := migrate.Options{
		Target: conn, Timezone: zone, Loc: loc, DryRun: *dry, Force: *force, SkipAssumed: *skipAssumed, AllHistory: *allHistory,
	}
	for _, k := range strings.Split(*disable, ",") {
		if k = strings.TrimSpace(k); k != "" {
			opts.DisableKinds = append(opts.DisableKinds, k)
		}
	}
	if !*noRefresh {
		opts.RefreshItem = func(ctx context.Context, id int64) error {
			item, err := cat.GetItem(ctx, id)
			if err != nil {
				return err
			}
			if missingKey(cfg, item.Source) {
				return migrate.ErrSkip
			}
			_, err = cat.RefreshItem(ctx, item)
			return err
		}
		opts.SyncSeasons = func(ctx context.Context, id int64) error {
			if cfg.TMDBKey == "" {
				return migrate.ErrSkip
			}
			item, err := cat.GetItem(ctx, id)
			if err != nil {
				return err
			}
			return cat.SyncSeasons(ctx, item, catalog.Force)
		}
	}
	base := providers.NewClient(providerCache)
	opts.LoadMappings = func(ctx context.Context) error {
		store := mappings.New(conn, queries, base)
		if *mapFile != "" {
			f, err := os.Open(*mapFile)
			if err != nil {
				return err
			}
			defer f.Close()
			_, err = store.Load(ctx, f)
			return err
		}
		_, err := store.Refresh(ctx)
		return err
	}

	if *assumedOnly {
		res, err := migrate.ApplyAssumed(ctx, opts)
		if err != nil {
			return err
		}
		rep := &migrate.Report{Counts: map[string]int{"assumed episodes": res.Episodes}, Assumed: res.Assumed, Deferred: res.Deferred}
		fmt.Fprint(out, rep.Summary())
		return nil
	}

	src, err := sql.Open("sqlite", "file:"+(&url.URL{Path: *from}).EscapedPath()+"?mode=ro")
	if err != nil {
		return err
	}
	defer src.Close()
	opts.Source = src
	rep, err := migrate.Run(ctx, opts)
	if rep != nil {
		fmt.Fprint(out, rep.Summary())
	}
	if err == nil && !*dry && !*noRefresh && cfg.TMDBKey != "" {
		// Django's release events are not copied; rebuild the calendar now
		// instead of waiting for the periodic job.
		n, rerr := cat.RefreshAllReleases(ctx)
		fmt.Fprintf(out, "releases refreshed for %d items (err: %v)\n", n, rerr)
	}
	return err
}

// missingKey reports whether the provider key an item's source needs is not configured.
func missingKey(cfg config.Config, source string) bool {
	switch source {
	case catalog.SourceTMDB:
		return cfg.TMDBKey == ""
	case catalog.SourceMAL:
		return cfg.MALKey == ""
	case catalog.SourceHardcover:
		return cfg.HardcoverKey == ""
	case catalog.SourceComicVine:
		return cfg.ComicVineKey == ""
	case catalog.SourceIGDB:
		return cfg.IGDBID == "" || cfg.IGDBSecret == ""
	case catalog.SourceManual:
		return true
	}
	return false
}
