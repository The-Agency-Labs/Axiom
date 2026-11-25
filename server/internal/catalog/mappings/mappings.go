// Package mappings keeps the Fribb/anime-lists dataset (MyAnimeList and
// AniList ids linked to TMDB and TVDB ids, with the TMDB season and episode
// offset of each cour) in the anime_mappings table. It is refreshed weekly
// and is how anime is told apart from live action.
package mappings

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/the-agency-labs/encompass/server/internal/db"
	"github.com/the-agency-labs/encompass/server/internal/jobs"
	"github.com/the-agency-labs/encompass/server/internal/providers"
)

const (
	// DatasetURL is the full Fribb dataset.
	DatasetURL = "https://raw.githubusercontent.com/Fribb/anime-lists/master/anime-list-full.json"
	// RefreshedKey is the kv key holding the time of the last refresh (RFC 3339).
	RefreshedKey = "catalog:anime_mappings_refreshed"
	// MaxAge is how old the data may get before the daily job downloads it again.
	MaxAge = 7 * 24 * time.Hour

	batchSize     = 2000
	providerLabel = "Anime mappings"
)

// Mapping is one row of the dataset.
type Mapping = db.AnimeMapping

// TMDBIDs are the TMDB ids that are anime, as decimal strings.
type TMDBIDs struct {
	Shows map[string]struct{}
	Films map[string]struct{}
}

// Store reads and refreshes the mapping table. Safe for concurrent use.
type Store struct {
	conn *sql.DB
	q    *db.Queries
	base *providers.Client
	url  string
	now  func() time.Time

	refreshMu sync.Mutex // one refresh at a time

	mu     sync.Mutex
	idsFor string // refresh stamp the cached ids belong to
	ids    *TMDBIDs
}

// New returns a Store that downloads through base.
func New(conn *sql.DB, q *db.Queries, base *providers.Client) *Store {
	return &Store{conn: conn, q: q, base: base, url: DatasetURL,
		now: func() time.Time { return time.Now().UTC() }}
}

// RegisterJobs schedules the daily staleness check (the download itself
// happens at most weekly).
func (s *Store) RegisterJobs(m *jobs.Manager) {
	m.Every("anime-mappings-refresh", 24*time.Hour, func(ctx context.Context) error {
		_, err := s.RefreshIfStale(ctx)
		return err
	})
}

// flexInt decodes the loosely typed ids of the dataset: a number, a numeric
// string, or a list (first element wins). Anything else is "no value".
type flexInt struct {
	Val   int64
	Valid bool
}

func (f *flexInt) UnmarshalJSON(b []byte) error {
	*f = flexInt{}
	b = bytes.TrimSpace(b)
	if len(b) == 0 {
		return nil
	}
	switch b[0] {
	case 'n':
		// null: no value
	case '[':
		var list []json.RawMessage
		if json.Unmarshal(b, &list) != nil || len(list) == 0 {
			return nil
		}
		return f.UnmarshalJSON(list[0])
	case '"':
		var s string
		if json.Unmarshal(b, &s) != nil {
			return nil
		}
		if n, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64); err == nil {
			*f = flexInt{n, true}
		}
	default:
		var n float64
		if json.Unmarshal(b, &n) == nil {
			*f = flexInt{int64(n), true}
		}
	}
	return nil
}

func (f flexInt) ptr() *int64 {
	if !f.Valid {
		return nil
	}
	v := f.Val
	return &v
}

type entry struct {
	MalID     flexInt         `json:"mal_id"`
	AnilistID flexInt         `json:"anilist_id"`
	Type      any             `json:"type"`
	TMDB      json.RawMessage `json:"themoviedb_id"`
	Season    json.RawMessage `json:"season"`
	Offset    json.RawMessage `json:"episode_offset"`
	TVDB      flexInt         `json:"tvdb_id"`
}

// row is one inserted row; the keys are the column names read by
// InsertAnimeMappingBatch.
type row struct {
	MalID      *int64 `json:"mal_id"`
	AnilistID  *int64 `json:"anilist_id"`
	Kind       string `json:"kind"`
	TmdbTv     *int64 `json:"tmdb_tv"`
	TmdbMovie  *int64 `json:"tmdb_movie"`
	TmdbSeason *int64 `json:"tmdb_season"`
	TmdbOffset int64  `json:"tmdb_offset"`
	TvdbID     *int64 `json:"tvdb_id"`
}

// toRow converts a dataset entry; ok is false for entries with neither id.
func toRow(e entry) (row, bool) {
	if e.MalID.Val == 0 && e.AnilistID.Val == 0 {
		return row{}, false
	}
	var tmdb struct {
		TV    flexInt `json:"tv"`
		Movie flexInt `json:"movie"`
	}
	var season, offset struct {
		TMDB flexInt `json:"tmdb"`
	}
	// A themoviedb_id that is not an object just means "no TMDB ids".
	_ = json.Unmarshal(e.TMDB, &tmdb)
	_ = json.Unmarshal(e.Season, &season)
	_ = json.Unmarshal(e.Offset, &offset)
	kind, _ := e.Type.(string)
	if r := []rune(kind); len(r) > 20 {
		kind = string(r[:20])
	}
	return row{
		MalID: e.MalID.ptr(), AnilistID: e.AnilistID.ptr(), Kind: kind,
		TmdbTv: tmdb.TV.ptr(), TmdbMovie: tmdb.Movie.ptr(),
		TmdbSeason: season.TMDB.ptr(), TmdbOffset: offset.TMDB.Val,
		TvdbID: e.TVDB.ptr(),
	}, true
}

// Refresh downloads the dataset and replaces the table in one transaction.
// It returns the number of rows loaded.
func (s *Store) Refresh(ctx context.Context) (int, error) {
	s.refreshMu.Lock()
	defer s.refreshMu.Unlock()
	body, err := s.base.Do(ctx, providers.Request{Provider: providerLabel, URL: s.url})
	if err != nil {
		return 0, err
	}
	n, err := s.Load(ctx, bytes.NewReader(body))
	debug.FreeOSMemory() // the download buffer is megabytes; hand it back to the OS
	return n, err
}

// Load replaces the table with the dataset read from r, decoding the JSON
// array one entry at a time and inserting in batches. On any error the old
// data stays.
func (s *Store) Load(ctx context.Context, r io.Reader) (int, error) {
	tx, err := s.conn.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	q := s.q.WithTx(tx)
	if err := q.DeleteAnimeMappings(ctx); err != nil {
		return 0, err
	}

	dec := json.NewDecoder(r)
	if tok, err := dec.Token(); err != nil {
		return 0, fmt.Errorf("anime mappings: %w", err)
	} else if d, ok := tok.(json.Delim); !ok || d != '[' {
		return 0, errors.New("anime mappings: dataset is not a JSON array")
	}
	total := 0
	batch := make([]row, 0, batchSize)
	flush := func() error {
		if len(batch) == 0 {
			return nil
		}
		data, err := json.Marshal(batch)
		if err != nil {
			return err
		}
		if err := q.InsertAnimeMappingBatch(ctx, string(data)); err != nil {
			return err
		}
		total += len(batch)
		batch = batch[:0]
		return nil
	}
	for dec.More() {
		var e entry
		if err := dec.Decode(&e); err != nil {
			var typeErr *json.UnmarshalTypeError
			if errors.As(err, &typeErr) {
				continue // not an object
			}
			return 0, fmt.Errorf("anime mappings: %w", err)
		}
		rw, ok := toRow(e)
		if !ok {
			continue
		}
		batch = append(batch, rw)
		if len(batch) == batchSize {
			if err := flush(); err != nil {
				return 0, err
			}
		}
	}
	if _, err := dec.Token(); err != nil {
		return 0, fmt.Errorf("anime mappings: %w", err)
	}
	if err := flush(); err != nil {
		return 0, err
	}
	stamp := s.now().Format(time.RFC3339Nano)
	if err := q.SetKV(ctx, db.SetKVParams{Key: RefreshedKey, Value: stamp, UpdatedAt: stamp}); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	slog.InfoContext(ctx, "loaded anime mappings", "rows", total)
	return total, nil
}

// EnsureLoaded loads the dataset the first time it is needed. A failed
// download is returned; callers may carry on with an empty table.
func (s *Store) EnsureLoaded(ctx context.Context) error {
	exists, err := s.q.AnimeMappingsExist(ctx)
	if err != nil || exists {
		return err
	}
	if _, err := s.Refresh(ctx); err != nil {
		slog.WarnContext(ctx, "anime mappings unavailable", "err", err)
		return err
	}
	return nil
}

func (s *Store) refreshedAt(ctx context.Context) (string, error) {
	v, err := s.q.GetKV(ctx, RefreshedKey)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return v, err
}

// IsStale reports whether the table is empty or older than a week.
func (s *Store) IsStale(ctx context.Context) (bool, error) {
	stamp, err := s.refreshedAt(ctx)
	if err != nil || stamp == "" {
		return true, err
	}
	exists, err := s.q.AnimeMappingsExist(ctx)
	if err != nil || !exists {
		return true, err
	}
	at, err := time.Parse(time.RFC3339, stamp)
	if err != nil {
		return true, nil
	}
	return s.now().Sub(at) > MaxAge, nil
}

// RefreshIfStale refreshes when IsStale; it returns the rows loaded (0 when fresh).
func (s *Store) RefreshIfStale(ctx context.Context) (int, error) {
	stale, err := s.IsStale(ctx)
	if err != nil || !stale {
		return 0, err
	}
	return s.Refresh(ctx)
}

func nullInt(id int64) sql.NullInt64 { return sql.NullInt64{Int64: id, Valid: true} }

func one(m Mapping, err error) (*Mapping, error) {
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &m, nil
}

// Lookup finds the mapping for a MAL ("mal", the default) or AniList
// ("anilist") anime id, loading the dataset first if needed. It returns nil
// when there is none, or when mediaID is not a number.
func (s *Store) Lookup(ctx context.Context, source, mediaID string) (*Mapping, error) {
	_ = s.EnsureLoaded(ctx) // a failed download just means no mapping
	id, err := strconv.ParseInt(strings.TrimSpace(mediaID), 10, 64)
	if err != nil {
		return nil, nil
	}
	if source == "anilist" {
		return s.ByAniList(ctx, id)
	}
	return s.ByMAL(ctx, id)
}

// ByMAL returns the first mapping (lowest id) for a MAL id, or nil.
func (s *Store) ByMAL(ctx context.Context, id int64) (*Mapping, error) {
	return one(s.q.GetAnimeMappingByMAL(ctx, nullInt(id)))
}

// ByAniList returns the first mapping for an AniList id, or nil.
func (s *Store) ByAniList(ctx context.Context, id int64) (*Mapping, error) {
	return one(s.q.GetAnimeMappingByAniList(ctx, nullInt(id)))
}

// ForAniListMedia is the AniList importer's lookup: by AniList id, falling
// back to the MAL id (pass 0 when the media has none).
func (s *Store) ForAniListMedia(ctx context.Context, anilistID, malID int64) (*Mapping, error) {
	m, err := s.ByAniList(ctx, anilistID)
	if m != nil || err != nil || malID == 0 {
		return m, err
	}
	return s.ByMAL(ctx, malID)
}

// ByTMDBTV returns every mapping of a TMDB show, in dataset order.
func (s *Store) ByTMDBTV(ctx context.Context, id int64) ([]Mapping, error) {
	return s.q.ListAnimeMappingsByTMDBTV(ctx, nullInt(id))
}

// ByTMDBMovie returns every mapping of a TMDB film, in dataset order.
func (s *Store) ByTMDBMovie(ctx context.Context, id int64) ([]Mapping, error) {
	return s.q.ListAnimeMappingsByTMDBMovie(ctx, nullInt(id))
}

// ByTVDB returns every mapping of a TVDB id, in dataset order.
func (s *Store) ByTVDB(ctx context.Context, id int64) ([]Mapping, error) {
	return s.q.ListAnimeMappingsByTVDB(ctx, nullInt(id))
}

// OtherTMDBTV returns the mappings of a TMDB show except the row exceptID:
// the other cours that claim seasons of the same show.
func (s *Store) OtherTMDBTV(ctx context.Context, tmdbTV, exceptID int64) ([]Mapping, error) {
	all, err := s.ByTMDBTV(ctx, tmdbTV)
	if err != nil {
		return nil, err
	}
	out := all[:0]
	for _, m := range all {
		if m.ID != exceptID {
			out = append(out, m)
		}
	}
	return out, nil
}

// AnimeTMDBIDs returns the TMDB show and film ids that are anime, as sets of
// decimal strings. The result is cached until the next refresh.
func (s *Store) AnimeTMDBIDs(ctx context.Context) (TMDBIDs, error) {
	stamp, err := s.refreshedAt(ctx)
	if err != nil {
		return TMDBIDs{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ids != nil && s.idsFor == stamp {
		return *s.ids, nil
	}
	rows, err := s.q.ListAnimeTMDBIDs(ctx)
	if err != nil {
		return TMDBIDs{}, err
	}
	ids := TMDBIDs{Shows: map[string]struct{}{}, Films: map[string]struct{}{}}
	for _, r := range rows {
		if r.TmdbTv.Valid && r.TmdbTv.Int64 != 0 {
			ids.Shows[strconv.FormatInt(r.TmdbTv.Int64, 10)] = struct{}{}
		}
		if r.TmdbMovie.Valid && r.TmdbMovie.Int64 != 0 {
			ids.Films[strconv.FormatInt(r.TmdbMovie.Int64, 10)] = struct{}{}
		}
	}
	s.ids, s.idsFor = &ids, stamp
	return ids, nil
}
