// Command encompass runs the Encompass server.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/the-agency-labs/encompass/server/internal/config"
	"github.com/the-agency-labs/encompass/server/internal/db"
	"github.com/the-agency-labs/encompass/server/internal/httpapi"
	"github.com/the-agency-labs/encompass/server/web"
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stderr, nil)))
	if err := run(); err != nil {
		slog.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cfg, err := config.Load()
	if err != nil {
		return err
	}
	conn, err := db.Open(ctx, cfg.DatabasePath)
	if err != nil {
		return err
	}
	defer conn.Close()

	server := &httpapi.Server{Config: cfg, DB: conn, Queries: db.New(conn), Web: web.Files()}
	httpServer := &http.Server{
		Addr:              cfg.Addr,
		Handler:           server.Routes(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	errs := make(chan error, 1)
	go func() {
		slog.Info("listening", "addr", cfg.Addr)
		errs <- httpServer.ListenAndServe()
	}()
	select {
	case err := <-errs:
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
	case <-ctx.Done():
	}
	shutdown, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	return httpServer.Shutdown(shutdown)
}
