// Package db opens the SQLite database, applies migrations, and exposes the
// sqlc-generated queries.
package db

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"net/url"

	"github.com/pressly/goose/v3"
	_ "modernc.org/sqlite" // registers the "sqlite" driver
)

//go:embed migrations/*.sql
var migrations embed.FS

// Open opens (creating if needed) the database at path and migrates it.
//
// SQLite allows one writer at a time; busy_timeout makes concurrent writers
// wait instead of failing, and immediate transactions take the write lock
// up front so read-then-write transactions can't deadlock.
func Open(ctx context.Context, path string) (*sql.DB, error) {
	params := url.Values{}
	for _, pragma := range []string{
		"journal_mode(WAL)",
		"foreign_keys(ON)",
		"busy_timeout(10000)",
		"synchronous(NORMAL)",
		"cache_size(-4000)", // 4 MB page cache per connection
	} {
		params.Add("_pragma", pragma)
	}
	params.Set("_txlock", "immediate")
	conn, err := sql.Open("sqlite", "file:"+path+"?"+params.Encode())
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	conn.SetMaxOpenConns(4)
	conn.SetMaxIdleConns(2)
	if err := conn.PingContext(ctx); err != nil {
		conn.Close()
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	if err := Migrate(ctx, conn); err != nil {
		conn.Close()
		return nil, err
	}
	return conn, nil
}

// Migrate applies the embedded migrations.
func Migrate(ctx context.Context, conn *sql.DB) error {
	provider, err := goose.NewProvider(goose.DialectSQLite3, conn, mustSub(migrations, "migrations"))
	if err != nil {
		return fmt.Errorf("migrations: %w", err)
	}
	if _, err := provider.Up(ctx); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	return nil
}
