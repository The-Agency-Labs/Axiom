package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/the-agency-labs/encompass/server/internal/cache"
	"github.com/the-agency-labs/encompass/server/internal/catalog/mappings"
	"github.com/the-agency-labs/encompass/server/internal/config"
	"github.com/the-agency-labs/encompass/server/internal/db"
	"github.com/the-agency-labs/encompass/server/internal/jobs"
	"github.com/the-agency-labs/encompass/server/internal/providers/tmdb"
	"github.com/the-agency-labs/encompass/server/internal/showconv"
)

// showsToTVDB implements `encompass shows-to-tvdb [--dry-run]`: it moves every
// show tracked from TMDB onto its TheTVDB series (see internal/showconv). It
// is idempotent and transactional per show; back the database up first.
func showsToTVDB(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("shows-to-tvdb", flag.ContinueOnError)
	dry := fs.Bool("dry-run", false, "work out and report everything, change nothing in the library")
	reportFile := fs.String("report", "", "also write the full report as JSON to this file")
	mapFile := fs.String("mappings-file", "", "Fribb anime-list JSON to load instead of downloading it")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("unexpected argument %q", fs.Arg(0))
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if cfg.TVDBKey == "" {
		return errors.New("TVDB_API is not set")
	}
	loc, err := time.LoadLocation(cfg.TimeZone)
	if err != nil {
		return fmt.Errorf("TZ %q: %w", cfg.TimeZone, err)
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

	// The mapping tells anime from live action and finds TVDB ids.
	if store, ok := cat.Reg.Mappings.(*mappings.Store); ok {
		if *mapFile != "" {
			f, err := os.Open(*mapFile)
			if err != nil {
				return err
			}
			_, err = store.Load(ctx, f)
			f.Close()
			if err != nil {
				return err
			}
		} else if err := store.EnsureLoaded(ctx); err != nil {
			return fmt.Errorf("anime mappings: %w", err)
		}
	}

	cv := showconv.New(conn, queries, cat)
	if c, ok := cat.Reg.P.TMDB.(*tmdb.Client); ok {
		cv.Linker = c
	}
	cv.Progress = func(format string, args ...any) { fmt.Fprintf(os.Stderr, format+"\n", args...) }
	rep, err := cv.Run(ctx, showconv.Options{DryRun: *dry})
	if rep != nil {
		printReport(out, rep)
		if *reportFile != "" {
			if b, jerr := json.MarshalIndent(rep, "", "  "); jerr == nil {
				_ = os.WriteFile(*reportFile, b, 0o600)
			}
		}
	}
	return err
}

func printReport(w io.Writer, r *showconv.Report) {
	mode := "converted"
	if r.DryRun {
		mode = "would convert (dry run)"
	}
	fmt.Fprintf(w, "shows %s: %d, left on TMDB: %d\n", mode, len(r.Converted), len(r.Kept))
	fmt.Fprintf(w, "episode watches before: %d, after: %d\n", r.WatchesBefore, r.WatchesAfter)
	var unmapped, changed int
	methods := map[string]int{}
	for _, s := range r.Converted {
		unmapped += len(s.Unmapped)
		changed += len(s.Changed)
		for m, n := range s.Methods {
			methods[m] += n
		}
	}
	var names []string
	for m := range methods {
		names = append(names, m)
	}
	sort.Strings(names)
	var parts []string
	for _, m := range names {
		parts = append(parts, fmt.Sprintf("%s=%d", m, methods[m]))
	}
	fmt.Fprintf(w, "watches placed by: %s\n", strings.Join(parts, ", "))
	fmt.Fprintf(w, "watches kept on the closest episode (listed below): %d\n", unmapped)
	fmt.Fprintf(w, "entries whose progress changed: %d\n", changed)
	if !r.DryRun {
		fmt.Fprintf(w, "people: %d shared a TMDB id with a person already known, %d keyed by a new TMDB id, %d TVDB-only; %d people without credits removed\n",
			r.People.Merged, r.People.TMDBKeyed, r.People.TVDBOnly, r.PeopleRemoved)
	}
	fmt.Fprintln(w, "\nseasons (TMDB -> TVDB), entry season rows (before -> after):")
	for _, s := range r.Converted {
		fmt.Fprintf(w, "  %-45s %2d -> %-2d  rows %d -> %d  watches %d  tvdb %s (%s)\n",
			trunc(s.Title, 45), s.Seasons.Before, s.Seasons.After, s.Seasons.EntryBefore, s.Seasons.EntryAfter, s.Watches, s.TVDBID, s.Via)
	}
	if len(r.Kept) > 0 {
		fmt.Fprintln(w, "\nleft on TMDB:")
		for _, k := range r.Kept {
			fmt.Fprintf(w, "  %s (tmdb %s): %s\n", k.Title, k.TMDBID, k.Reason)
		}
	}
	if unmapped > 0 {
		fmt.Fprintln(w, "\nwatches without a TVDB twin (kept on the closest episode, with a note):")
		for _, s := range r.Converted {
			for _, u := range s.Unmapped {
				fmt.Fprintf(w, "  %s: watch %d (%s) S%02dE%02d -> S%02dE%02d: %s\n", u.Show, u.Watch, u.Source, u.FromSeason, u.FromEpisode, u.ToSeason, u.ToEpisode, u.Reason)
			}
		}
	}
	if changed > 0 {
		fmt.Fprintln(w, "\nentries whose progress changed:")
		for _, s := range r.Converted {
			for _, c := range s.Changed {
				fmt.Fprintf(w, "  %s: entry %d run %d: %d -> %d\n", s.Title, c.Entry, c.Run, c.Before, c.After)
			}
		}
	}
	for _, s := range r.Converted {
		for _, n := range s.Notes {
			fmt.Fprintf(w, "note: %s: %s\n", s.Title, n)
		}
	}
	for _, c := range r.StatusChanges {
		fmt.Fprintf(w, "STATUS CHANGED: %s\n", c)
	}
}

func trunc(s string, n int) string {
	r := []rune(s)
	if len(r) > n {
		return string(r[:n-1]) + "…"
	}
	return s
}
