// Command axiom runs the Axiom server.
//
// Usage:
//
//	axiom                          serve (default)
//	axiom serve
//	axiom createuser <name> [--admin]   password is read from stdin
//	axiom healthcheck              GET /health on the local server, exit 0/1
//	axiom migrate-django --from <django.db> [--dry-run] [--force]   one-shot data migration
//	axiom shows-to-tvdb [--dry-run]   move shows tracked from TMDB onto TheTVDB (idempotent; back up first)
package main

import (
	"bufio"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/the-agency-labs/axiom/server/internal/auth"
	"github.com/the-agency-labs/axiom/server/internal/cache"
	"github.com/the-agency-labs/axiom/server/internal/catalog"
	"github.com/the-agency-labs/axiom/server/internal/catalog/mappings"
	"github.com/the-agency-labs/axiom/server/internal/config"
	"github.com/the-agency-labs/axiom/server/internal/db"
	"github.com/the-agency-labs/axiom/server/internal/httpapi"
	"github.com/the-agency-labs/axiom/server/internal/integrations"
	"github.com/the-agency-labs/axiom/server/internal/integrations/jellyfin"
	"github.com/the-agency-labs/axiom/server/internal/jobs"
	"github.com/the-agency-labs/axiom/server/internal/library"
	"github.com/the-agency-labs/axiom/server/internal/providers"
	"github.com/the-agency-labs/axiom/server/internal/providers/anilist"
	"github.com/the-agency-labs/axiom/server/internal/providers/bgg"
	"github.com/the-agency-labs/axiom/server/internal/providers/comicvine"
	"github.com/the-agency-labs/axiom/server/internal/providers/hardcover"
	"github.com/the-agency-labs/axiom/server/internal/providers/igdb"
	"github.com/the-agency-labs/axiom/server/internal/providers/mal"
	"github.com/the-agency-labs/axiom/server/internal/providers/mangaupdates"
	"github.com/the-agency-labs/axiom/server/internal/providers/openlibrary"
	"github.com/the-agency-labs/axiom/server/internal/providers/tmdb"
	"github.com/the-agency-labs/axiom/server/internal/providers/tvdb"
	"github.com/the-agency-labs/axiom/server/internal/reading"
	"github.com/the-agency-labs/axiom/server/web"
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stderr, nil)))
	cmd, args := "serve", []string(nil)
	if len(os.Args) > 1 {
		cmd, args = os.Args[1], os.Args[2:]
	}
	var err error
	switch cmd {
	case "serve":
		err = serve()
	case "createuser":
		err = createUser(args, os.Stdin)
	case "healthcheck":
		err = healthcheck()
	case "migrate-django":
		err = migrateDjango(args, os.Stdout)
	case "shows-to-tvdb":
		err = showsToTVDB(args, os.Stdout)
	case "refresh-items":
		err = refreshItems(args, os.Stdout)
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\nusage: axiom [serve | createuser <username> [--admin] | healthcheck | migrate-django --from <django.db> | shows-to-tvdb [--dry-run] | refresh-items --source <source>]\n", cmd)
		os.Exit(2)
	}
	if err != nil {
		slog.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func serve() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cfg, err := config.Load()
	if err != nil {
		return err
	}
	loc, err := time.LoadLocation(cfg.TimeZone)
	if err != nil {
		return fmt.Errorf("TZ %q: %w", cfg.TimeZone, err)
	}
	if cfg.TVDBKey == "" {
		slog.Warn("TVDB_API is not set: shows (tv and anime) are TheTVDB shows, so they cannot be searched, added or synced")
	}
	conn, err := db.Open(ctx, cfg.DatabasePath)
	if err != nil {
		return err
	}
	defer conn.Close()
	queries := db.New(conn)

	authSvc := auth.NewService(queries, cfg.SessionDays)
	jobMgr := jobs.New(conn, queries, loc)
	providerCache := cache.New(queries, cache.DefaultEntries)
	providerCache.RegisterJobs(jobMgr)
	cat := newCatalog(cfg, conn, queries, providerCache, jobMgr)
	lib := library.New(conn, queries, cat)
	lib.Loc = loc // "today", released episodes and reading days use TZ
	jf := jellyfin.NewService(conn, queries, cfg, cat, lib, providerCache, jobMgr)
	jf.RegisterJobs(jobMgr)
	// Importers: AniList poll, Goodreads upload and Steam sync jobs.
	suite := integrations.New(integrations.Deps{Config: cfg, Queries: queries, Catalog: cat, Library: lib, Cache: providerCache})
	suite.RegisterJobs(jobMgr)
	// What each app tracks is current from the start, not only after its next sync.
	go func() {
		jf.RefreshLinks(ctx)
		suite.AniList.RefreshLinks(ctx)
		suite.Steam.RefreshLinks(ctx)
		// Casts cleared by a migration come back now rather than in six hours.
		if n, err := cat.BackfillBackdrops(ctx); err != nil {
			slog.Warn("banner art backfill", "err", err)
		} else if n > 0 {
			slog.Info("banner art backfill", "refreshed", n)
		}
		if _, err := cat.BackfillCredits(ctx, catalog.CreditsBackfillLimit); err != nil {
			slog.Warn("credits backfill", "err", err)
		}
	}()
	// Kindle documents never tried against the book providers get linked in the background.
	readingSvc := reading.New(conn, queries, cat, lib)
	readingSvc.RegisterJobs(jobMgr)
	// Books read on a device carry its page count (idempotent; covers books linked before it did).
	if err := readingSvc.MatchAllDevicePages(ctx); err != nil {
		slog.Warn("matching device page counts", "err", err)
	}
	jobMgr.Every("session-cleanup", time.Hour, func(ctx context.Context) error {
		_, err := authSvc.CleanupSessions(ctx)
		return err
	})

	server := &httpapi.Server{
		Config: cfg, DB: conn, Queries: queries, Web: web.Files(),
		Auth: authSvc, Jobs: jobMgr, Cache: providerCache,
		Catalog: cat, Library: lib,
	}
	httpServer := &http.Server{
		Addr:              cfg.Addr,
		Handler:           server.Routes(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	jobsDone := make(chan struct{})
	go func() {
		defer close(jobsDone)
		if err := jobMgr.Run(ctx); err != nil {
			slog.Error("jobs stopped", "err", err)
		}
	}()

	errs := make(chan error, 1)
	go func() {
		slog.Info("listening", "addr", cfg.Addr)
		errs <- httpServer.ListenAndServe()
	}()
	var serveErr error
	select {
	case err := <-errs:
		if !errors.Is(err, http.ErrServerClosed) {
			serveErr = err
		}
	case <-ctx.Done():
	}
	stop() // also stops the jobs if the listener failed
	shutdown, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := httpServer.Shutdown(shutdown); err != nil && serveErr == nil {
		serveErr = err
	}
	select {
	case <-jobsDone:
	case <-shutdown.Done():
		slog.Warn("jobs did not stop in time")
	}
	return serveErr
}

// newCatalog builds the provider clients and the catalog on them, and
// registers the periodic jobs: the anime mapping refresh, the credits
// backfill and the release refresh. The HTTP layer gets the same catalog once
// its routes need it.
func newCatalog(cfg config.Config, conn *sql.DB, queries *db.Queries, providerCache *cache.Cache, jobMgr *jobs.Manager) *catalog.Catalog {
	base := providers.NewClient(providerCache)
	mapStore := mappings.New(conn, queries, base)
	reg := catalog.NewRegistry(catalog.Providers{
		TMDB:         tmdb.New(base, cfg),
		TVDB:         tvdb.New(base, cfg),
		AniList:      anilist.New(base, cfg),
		MAL:          mal.New(base, cfg),
		MangaUpdates: mangaupdates.New(base, cfg),
		Hardcover:    hardcover.New(base, cfg),
		OpenLibrary:  openlibrary.New(base, cfg),
		ComicVine:    comicvine.New(base, cfg),
		IGDB:         igdb.New(base, cfg),
		BGG:          bgg.New(base, cfg),
	}, mapStore)
	cat := catalog.New(conn, queries, reg)
	cat.TMDBCredits = catalog.NewTMDBCredits(base, cfg)
	if loc, err := time.LoadLocation(cfg.TimeZone); err == nil {
		cat.Loc = loc
	}
	mapStore.RegisterJobs(jobMgr)
	cat.RegisterJobs(jobMgr)
	return cat
}

// createUser implements `axiom createuser <username> [--admin]`.
func createUser(args []string, stdin io.Reader) error {
	var username string
	var admin bool
	for _, a := range args {
		switch {
		case a == "--admin":
			admin = true
		case strings.HasPrefix(a, "-"):
			return fmt.Errorf("unknown flag %q", a)
		case username == "":
			username = a
		default:
			return fmt.Errorf("unexpected argument %q", a)
		}
	}
	if username == "" {
		return errors.New("usage: axiom createuser <username> [--admin] (password on stdin)")
	}
	password, err := bufio.NewReader(stdin).ReadString('\n')
	if err != nil && !(errors.Is(err, io.EOF) && password != "") {
		return fmt.Errorf("read password from stdin: %w", err)
	}
	password = strings.TrimRight(password, "\r\n")
	if len(password) < 8 {
		return errors.New("password must be at least 8 characters")
	}

	cfg, err := config.Load()
	if err != nil {
		return err
	}
	ctx := context.Background()
	conn, err := db.Open(ctx, cfg.DatabasePath)
	if err != nil {
		return err
	}
	defer conn.Close()
	user, err := auth.NewService(db.New(conn), cfg.SessionDays).CreateUser(ctx, username, password, admin)
	if err != nil {
		return fmt.Errorf("create user: %w", err)
	}
	slog.Info("user created", "id", user.ID, "username", user.Username, "admin", admin)
	return nil
}

// healthcheck probes the local server; used by the Docker HEALTHCHECK, since
// the distroless image has no shell or wget.
func healthcheck() error {
	addr := os.Getenv("ADDR")
	if addr == "" {
		addr = ":8000"
	}
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("ADDR %q: %w", addr, err)
	}
	client := http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get("http://127.0.0.1:" + port + "/health")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("health returned %s", resp.Status)
	}
	return nil
}
