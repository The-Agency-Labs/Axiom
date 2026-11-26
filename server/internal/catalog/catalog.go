// Package catalog owns the shared side of the library: items and their ids,
// provider dispatch, seasons and episodes, anime episode merging, credits and
// upcoming releases. It replaces services.get_media_metadata/search, the
// catalog app (anime.py, credits.py) and the calendar of the Django app.
//
// Provider calls go through small interfaces (see providers.go) so tests can
// stub them; the real clients under internal/providers satisfy them as is.
package catalog

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/the-agency-labs/axiom/server/internal/db"
)

// Item is a row of the items table.
type Item = db.Item

// Kinds of media an item can have.
const (
	KindTV         = "tv"
	KindAnime      = "anime"
	KindMovie      = "movie"
	KindAnimeMovie = "anime_movie"
	KindManga      = "manga"
	KindComic      = "comic"
	KindBook       = "book"
	KindGame       = "game"
	KindBoardgame  = "boardgame"
)

// Kinds lists every kind.
var Kinds = []string{KindTV, KindAnime, KindMovie, KindAnimeMovie, KindManga, KindComic, KindBook, KindGame, KindBoardgame}

// Metadata sources.
const (
	SourceTMDB         = "tmdb"
	SourceTVDB         = "tvdb"
	SourceMAL          = "mal"
	SourceAniList      = "anilist"
	SourceMangaUpdates = "mangaupdates"
	SourceComicVine    = "comicvine"
	SourceHardcover    = "hardcover"
	SourceOpenLibrary  = "openlibrary"
	SourceIGDB         = "igdb"
	SourceBGG          = "bgg"
	SourceManual       = "manual"
)

var (
	// ErrNotFound means there is no such item.
	ErrNotFound = errors.New("catalog: not found")
	// ErrUnsupported means no provider serves that (kind, source).
	ErrUnsupported = errors.New("catalog: unsupported kind or source")
)

// IsShow reports whether a kind is tracked by seasons and episodes (as
// opposed to a single progress count).
func IsShow(kind string) bool { return kind == KindTV || kind == KindAnime }

// IsMovie reports whether a kind is a single-viewing film.
func IsMovie(kind string) bool { return kind == KindMovie || kind == KindAnimeMovie }

// HasEpisodes reports whether an item has seasons and episodes: a show whose
// metadata source is TheTVDB (every show is) or, for a show with no TVDB
// match left over from before the switch, TMDB. MAL/AniList-only anime are
// count-based.
func HasEpisodes(item Item) bool {
	return IsShow(item.Kind) && (item.Source == SourceTVDB || item.Source == SourceTMDB)
}

// Catalog is the item store and its background jobs. Create it with New.
type Catalog struct {
	Conn *sql.DB
	Q    *db.Queries
	Reg  *Registry

	// TMDBCredits fetches (aggregate) credits from TMDB; nil disables TMDB credits.
	TMDBCredits TMDBCredits
	// Now and Loc are the clock and the zone "today" is read in (tests replace them).
	Now func() time.Time
	Loc *time.Location
}

// New returns a Catalog.
func New(conn *sql.DB, q *db.Queries, reg *Registry) *Catalog {
	return &Catalog{Conn: conn, Q: q, Reg: reg, Now: func() time.Time { return time.Now().UTC() }, Loc: time.UTC}
}

func (c *Catalog) now() time.Time { return c.Now().UTC() }

// Today is the current date in the configured zone, as YYYY-MM-DD.
func (c *Catalog) Today() string { return c.now().In(c.Loc).Format("2006-01-02") }

// Stamp formats a time the way every timestamp column stores it.
func Stamp(t time.Time) string { return t.UTC().Format(time.RFC3339) }

// tx runs fn in a transaction.
func (c *Catalog) tx(ctx context.Context, fn func(q *db.Queries) error) error {
	tx, err := c.Conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := fn(c.Q.WithTx(tx)); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

// GetItem returns an item by id (ErrNotFound when missing).
func (c *Catalog) GetItem(ctx context.Context, id int64) (Item, error) {
	item, err := c.Q.GetItem(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return Item{}, ErrNotFound
	}
	return item, err
}
