package httpapi

import (
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/the-agency-labs/encompass/server/internal/catalog"
	"github.com/the-agency-labs/encompass/server/internal/db"
	"github.com/the-agency-labs/encompass/server/internal/library"
	"github.com/the-agency-labs/encompass/server/internal/providers"
)

// libraryValidKind reports whether kind is a media_type the library endpoints accept.
func libraryValidKind(kind string) bool {
	for _, k := range catalog.Kinds {
		if k == kind {
			return true
		}
	}
	return false
}

// libraryKnownSource mirrors app.models.Sources.
func libraryKnownSource(source string) bool {
	switch source {
	case catalog.SourceTMDB, catalog.SourceMAL, catalog.SourceAniList, catalog.SourceMangaUpdates,
		catalog.SourceIGDB, catalog.SourceOpenLibrary, catalog.SourceHardcover, catalog.SourceComicVine,
		catalog.SourceBGG, catalog.SourceManual:
		return true
	}
	return false
}

func libraryIsAnime(kind string) bool {
	return kind == catalog.KindAnime || kind == catalog.KindAnimeMovie
}

func libraryNullStr(s sql.NullString) any {
	if !s.Valid {
		return nil
	}
	return s.String
}

func libraryStrOrNil(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func libraryNullFloat(f sql.NullFloat64) any {
	if !f.Valid {
		return nil
	}
	return f.Float64
}

func libraryFloatPtr(f *float64) any {
	if f == nil {
		return nil
	}
	return *f
}

func libraryIntPtr(n *int) any {
	if n == nil {
		return nil
	}
	return *n
}

// libraryItemJSON is serializers.item_dict.
func libraryItemJSON(it db.Item, season *int) map[string]any {
	var sn any
	if season != nil {
		sn = *season
	}
	return map[string]any{
		"id": it.ID, "media_id": it.ExternalID, "source": it.Source, "media_type": it.Kind,
		"title": it.Title, "image": it.Image, "season_number": sn, "episode_number": nil,
	}
}

// libraryEntryJSON is serializers.media_dict plus is_anime.
func libraryEntryJSON(e db.Entry, it db.Item) map[string]any {
	return map[string]any{
		"id": e.ID, "media_type": it.Kind, "item": libraryItemJSON(it, nil),
		"status": e.Status, "score": libraryNullFloat(e.Score), "progress": e.Progress,
		"progressed_at": libraryNullStr(e.ProgressedAt), "start_date": libraryNullStr(e.StartedAt),
		"end_date": libraryNullStr(e.FinishedAt), "notes": e.Notes, "created_at": e.CreatedAt,
		"is_anime": libraryIsAnime(it.Kind),
	}
}

// libraryHistoryJSON is api.library._history. Shows have no progress count,
// so their records carry null there (as the Django TV model did).
func libraryHistoryJSON(it db.Item, recs []library.HistoryRecord) []map[string]any {
	out := make([]map[string]any, 0, len(recs))
	for _, r := range recs {
		var progress any = r.Progress
		if catalog.HasEpisodes(it) {
			progress = nil
		}
		out = append(out, map[string]any{
			"date": r.At, "type": r.Type, "status": r.Status, "progress": progress, "score": libraryFloatPtr(r.Score),
		})
	}
	return out
}

// libraryError maps domain and provider errors to API errors.
func libraryError(err error, source string) error {
	var apiErr *Error
	var provErr *providers.Error
	switch {
	case err == nil:
		return nil
	case errors.As(err, &apiErr):
		return err
	case errors.Is(err, library.ErrNoEntry), errors.Is(err, catalog.ErrNotFound):
		return Errorf(http.StatusNotFound, "Not Found")
	case errors.Is(err, library.ErrInvalidInput):
		return Errorf(http.StatusUnprocessableEntity, err.Error())
	case errors.Is(err, catalog.ErrUnsupported):
		return Errorf(http.StatusBadRequest, err.Error())
	case errors.Is(err, library.ErrNotShow), errors.Is(err, library.ErrNotMovie), errors.Is(err, library.ErrCountBased),
		errors.Is(err, catalog.ErrNotCountBased):
		return Errorf(http.StatusBadRequest, err.Error())
	case errors.As(err, &provErr):
		return Errorf(http.StatusBadGateway, source+" metadata unavailable")
	}
	return err
}

// libraryParseTime reads the date formats the API accepts.
func libraryParseTime(s string) (time.Time, error) {
	for _, layout := range []string{time.RFC3339, "2006-01-02T15:04:05", "2006-01-02T15:04", "2006-01-02 15:04:05", "2006-01-02 15:04", "2006-01-02"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC(), nil
		}
	}
	return time.Time{}, fmt.Errorf("invalid date %q", s)
}

func libraryQueryInt(r *http.Request, name string, def int) (int, error) {
	v := r.URL.Query().Get(name)
	if v == "" {
		return def, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0, Errorf(http.StatusUnprocessableEntity, name+" must be an integer")
	}
	return n, nil
}

func libraryPathInt(r *http.Request, name string) (int64, error) {
	n, err := strconv.ParseInt(r.PathValue(name), 10, 64)
	if err != nil {
		return 0, Errorf(http.StatusUnprocessableEntity, name+" must be an integer")
	}
	return n, nil
}

// libraryPathKind reads and validates the {media_type} path value.
func libraryPathKind(r *http.Request) (string, error) {
	kind := r.PathValue("media_type")
	if !libraryValidKind(kind) {
		return "", Errorf(http.StatusUnprocessableEntity, "media_type must be one of the library media types")
	}
	return kind, nil
}

// libraryDecodeOptional decodes a JSON body that may be empty.
func libraryDecodeOptional(r *http.Request, dst any) error {
	if r.ContentLength == 0 {
		return nil
	}
	return DecodeJSON(r, dst)
}
