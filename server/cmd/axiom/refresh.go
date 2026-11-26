package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"time"

	"github.com/the-agency-labs/axiom/server/internal/cache"
	"github.com/the-agency-labs/axiom/server/internal/config"
	"github.com/the-agency-labs/axiom/server/internal/db"
	"github.com/the-agency-labs/axiom/server/internal/jobs"
)

// refreshItems re-reads provider metadata for every item of one source, e.g.
// after a provider change (MAL English titles). It prints each title change.
func refreshItems(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("refresh-items", flag.ContinueOnError)
	source := fs.String("source", "", "provider source of the items to refresh (e.g. mal)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *source == "" || fs.NArg() > 0 {
		return fmt.Errorf("usage: axiom refresh-items --source <source>")
	}
	cfg, err := config.Load()
	if err != nil {
		return err
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
	cat := newCatalog(cfg, conn, queries, cache.New(queries, cache.DefaultEntries), jobs.New(conn, queries, loc))

	rows, err := conn.QueryContext(ctx, `SELECT id FROM items WHERE source = ? ORDER BY id`, *source)
	if err != nil {
		return err
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	var refreshed, failed int
	for _, id := range ids {
		item, err := cat.GetItem(ctx, id)
		if err != nil {
			return err
		}
		fresh, err := cat.RefreshItem(ctx, item)
		if err != nil {
			failed++
			fmt.Fprintf(out, "failed %d %q: %v\n", id, item.Title, err)
			continue
		}
		refreshed++
		if fresh.Title != item.Title {
			fmt.Fprintf(out, "%q -> %q\n", item.Title, fresh.Title)
		}
	}
	fmt.Fprintf(out, "refreshed %d %s items, %d failed\n", refreshed, *source, failed)
	return nil
}
