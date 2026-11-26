package httpapi

import (
	"context"
	"database/sql"
	"math"
	"strconv"
	"time"

	"github.com/the-agency-labs/axiom/server/internal/auth"
	"github.com/the-agency-labs/axiom/server/internal/catalog"
	"github.com/the-agency-labs/axiom/server/internal/db"
	"github.com/the-agency-labs/axiom/server/internal/library"
)

// overviewLoc is the user's time zone (UTC when unset or unknown).
func overviewLoc(u *db.User) *time.Location {
	if loc, err := time.LoadLocation(u.Timezone); err == nil && u.Timezone != "" {
		return loc
	}
	return time.UTC
}

// overviewEnabled returns the kinds the user tracks. A kind without a settings
// row is enabled.
func (s *Server) overviewEnabled(ctx context.Context, userID int64) (map[string]bool, error) {
	rows, err := s.Queries.ListUserMediaSettings(ctx, userID)
	if err != nil {
		return nil, err
	}
	on := map[string]bool{}
	for _, k := range catalog.Kinds {
		on[k] = true
	}
	for _, r := range rows {
		on[r.Kind] = r.Enabled != 0
	}
	return on, nil
}

// overviewRound rounds half to even, like Python's round().
func overviewRound(f float64) int { return int(math.RoundToEven(f)) }

// overviewNullStr is a nullable text column as JSON.
func overviewNullStr(s sql.NullString) any {
	if !s.Valid || s.String == "" {
		return nil
	}
	return s.String
}

func overviewNullFloat(f sql.NullFloat64) any {
	if !f.Valid {
		return nil
	}
	return f.Float64
}

// overviewTime parses a stored timestamp (RFC 3339) or date (YYYY-MM-DD, read
// in loc) and returns it in loc.
func overviewTime(s string, loc *time.Location) (time.Time, bool) {
	if s == "" {
		return time.Time{}, false
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t.In(loc), true
	}
	if t, err := time.ParseInLocation("2006-01-02", s, loc); err == nil {
		return t, true
	}
	return time.Time{}, false
}

func overviewStamp(t time.Time) string { return t.UTC().Format(time.RFC3339) }

// overviewItem is api.serializers.item_dict. mediaType overrides the kind (a
// show's season is a "season", a played episode an "episode").
func overviewItem(it db.Item, mediaType string, season, episode *int) map[string]any {
	if mediaType == "" {
		mediaType = it.Kind
	}
	return map[string]any{
		"id": it.ID, "media_id": it.ExternalID, "source": it.Source, "media_type": mediaType,
		"title": it.Title, "image": it.Image, "season_number": season, "episode_number": episode,
	}
}

// overviewEntry is api.serializers.media_dict.
func overviewEntry(r library.LibraryRow) map[string]any {
	e := r.Entry
	out := map[string]any{
		"id": e.ID, "media_type": r.Item.Kind, "item": overviewItem(r.Item, "", nil, nil),
		"status": e.Status, "score": overviewNullFloat(e.Score), "progress": e.Progress,
		"progressed_at": overviewNullStr(e.ProgressedAt), "start_date": overviewNullStr(e.StartedAt),
		"end_date": overviewNullStr(e.FinishedAt), "notes": e.Notes, "created_at": e.CreatedAt,
	}
	// Django derived a show's end date from its episodes; the latest play stands in for it.
	if catalog.HasEpisodes(r.Item) && !e.FinishedAt.Valid {
		out["end_date"] = overviewNullStr(e.ProgressedAt)
	}
	return out
}

// overviewIntParam reads an optional integer query parameter.
func overviewIntParam(q map[string][]string, name string, def int) (int, error) {
	v := q[name]
	if len(v) == 0 || v[0] == "" {
		return def, nil
	}
	n, err := strconv.Atoi(v[0])
	if err != nil {
		return 0, Errorf(422, name+" must be an integer")
	}
	return n, nil
}

func overviewUser(ctx context.Context) db.User { return *auth.User(ctx) }

func overviewClamp(n, lo, hi int) int { return max(lo, min(n, hi)) }
