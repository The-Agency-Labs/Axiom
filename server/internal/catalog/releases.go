package catalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/the-agency-labs/axiom/server/internal/db"
	"github.com/the-agency-labs/axiom/server/internal/providers"
)

// Releases port src/events (the calendar) minus notifications: they fill the
// releases table with the air/publication time of every episode, chapter,
// issue or title of what is tracked, for the iCal feed.
//
// Differences from Django: no TVMaze airstamps (air times are the TMDB air
// date at the 11:59:59 UTC "date only" sentinel), and titles with an unknown
// date store no release at all instead of a far-future/far-past sentinel row.

// dateOnlyHour/minute/second is the time of day a date-only release gets
// (events.models.SentinelDatetime), so clients can tell it carries no time.
const (
	sentinelHour   = 11
	sentinelMinute = 59
	sentinelSecond = 59
)

// ParseReleaseDate parses "YYYY", "YYYY-MM" or "YYYY-MM-DD" into the
// date-only release time (date_parser in events/calendar/helpers.py).
func ParseReleaseDate(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	switch strings.Count(s, "-") {
	case 0:
		s += "-01-01"
	case 1:
		s += "-01"
	}
	d, err := time.Parse("2006-01-02", s)
	if err != nil {
		return time.Time{}, err
	}
	return time.Date(d.Year(), d.Month(), d.Day(), sentinelHour, sentinelMinute, sentinelSecond, 0, time.UTC), nil
}

// ReleasedEpisodes returns the episode numbers of one season (given in
// position order) that have aired by today (YYYY-MM-DD). An undated episode
// counts as aired when a later episode of the season has (the provider simply
// has no date for it, issue #884 in the Django app); otherwise it is unreleased.
func ReleasedEpisodes(eps []db.ItemEpisode, today string) map[int64]bool {
	aired := func(e db.ItemEpisode) bool {
		if !e.AirDate.Valid {
			return false
		}
		t, err := ParseReleaseDate(e.AirDate.String)
		return err == nil && t.Format("2006-01-02") <= today
	}
	latest := -1
	for i, e := range eps {
		if aired(e) {
			latest = i
		}
	}
	out := map[int64]bool{}
	for i, e := range eps {
		if aired(e) || (i < latest && !validDate(e.AirDate)) {
			out[e.Number] = true
		}
	}
	return out
}

func validDate(s sql.NullString) bool {
	if !s.Valid {
		return false
	}
	_, err := ParseReleaseDate(s.String)
	return err == nil
}

// release is one row to store.
type release struct {
	season, number sql.NullInt64
	at             time.Time
}

func relNum(n int) sql.NullInt64 { return sql.NullInt64{Int64: int64(n), Valid: true} }

// RefreshReleases recomputes the releases of one item from its provider data
// and replaces the stored rows. TMDB shows are synced first (SyncSeasons).
func (c *Catalog) RefreshReleases(ctx context.Context, item Item) error {
	if item.Source == SourceManual {
		return nil
	}
	var rows []release
	var err error
	switch {
	case HasEpisodes(item):
		rows, err = c.episodeReleases(ctx, item)
	case item.Kind == KindComic:
		rows, err = c.comicReleases(ctx, item)
	case item.Kind == KindAnime && (item.Source == SourceAniList || item.Source == SourceMAL):
		rows, err = c.animeReleases(ctx, item)
	default:
		rows, err = c.otherReleases(ctx, item)
	}
	if err != nil {
		return err
	}
	return c.tx(ctx, func(q *db.Queries) error {
		if err := q.DeleteItemReleases(ctx, item.ID); err != nil {
			return err
		}
		for _, r := range rows {
			if err := q.InsertRelease(ctx, db.InsertReleaseParams{
				ItemID: item.ID, Season: r.season, Number: r.number, At: Stamp(r.at),
			}); err != nil {
				return err
			}
		}
		return nil
	})
}

// episodeReleases is process_tv: one release per episode of every season.
func (c *Catalog) episodeReleases(ctx context.Context, item Item) ([]release, error) {
	if err := c.SyncSeasons(ctx, item); err != nil {
		return nil, err
	}
	eps, err := c.Q.ListItemEpisodes(ctx, item.ID)
	if err != nil {
		return nil, err
	}
	var rows []release
	bySeason := map[int64][]db.ItemEpisode{}
	var order []int64
	for _, e := range eps {
		if _, ok := bySeason[e.Season]; !ok {
			order = append(order, e.Season)
		}
		bySeason[e.Season] = append(bySeason[e.Season], e)
	}
	for _, s := range order {
		list := bySeason[s]
		resolved := resolveEpisodeTimes(list, c.now())
		for i, e := range list {
			if at, ok := resolved[i]; ok {
				rows = append(rows, release{season: sql.NullInt64{Int64: e.Season, Valid: true}, number: relNum(int(e.Number)), at: at})
			}
		}
	}
	return rows, nil
}

// resolveEpisodeTimes ports resolve_episode_datetimes: dated episodes keep
// their time; an undated one before an episode that has already aired takes
// the nearest later aired time; the rest stay unknown (absent).
func resolveEpisodeTimes(list []db.ItemEpisode, now time.Time) map[int]time.Time {
	times := make([]*time.Time, len(list))
	latestAired := -1
	for i, e := range list {
		if !e.AirDate.Valid {
			continue
		}
		if t, err := ParseReleaseDate(e.AirDate.String); err == nil {
			times[i] = &t
			if !t.After(now) {
				latestAired = i
			}
		}
	}
	out := map[int]time.Time{}
	for i := range list {
		switch {
		case times[i] != nil:
			out[i] = *times[i]
		case i < latestAired:
			for j := i + 1; j < len(list); j++ {
				if times[j] != nil && !times[j].After(now) {
					out[i] = *times[j]
					break
				}
			}
		}
	}
	return out
}

// otherReleases is process_other: the date of the last known episode/chapter
// (anime, manga: end date), film/game release date, book publication date.
func (c *Catalog) otherReleases(ctx context.Context, item Item) ([]release, error) {
	meta, err := c.Reg.Metadata(ctx, item.Kind, item.Source, item.ExternalID)
	if err != nil {
		return nil, err
	}
	details := asMap(meta["details"])
	dateKey := map[string]string{
		KindMovie: "release_date", KindAnimeMovie: "release_date", KindAnime: "end_date", KindManga: "end_date",
		KindGame: "release_date", KindBook: "publish_date", KindBoardgame: "year",
	}[item.Kind]
	_, hasKey := details[dateKey]
	content := providers.Int(meta, "max_progress")
	text := dateText(details[dateKey])
	var rows []release
	switch {
	case hasKey && content > 0:
		if text == "" {
			return nil, nil
		}
		at, err := ParseReleaseDate(text)
		if err != nil {
			slog.WarnContext(ctx, "catalog: invalid release date", "item", item.ID, "date", text)
			return nil, nil
		}
		r := release{at: at}
		if !IsMovie(item.Kind) {
			r.number = relNum(content)
		}
		rows = append(rows, r)
	case item.Kind == KindGame && text != "":
		at, err := ParseReleaseDate(text)
		if err != nil {
			return nil, nil
		}
		rows = append(rows, release{at: at})
	}
	return rows, nil
}

// dateText renders a metadata date (a string, or a year as a number).
func dateText(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case float64:
		if x > 0 {
			return strconv.Itoa(int(x))
		}
	case int:
		if x > 0 {
			return strconv.Itoa(x)
		}
	}
	return ""
}

// comicReleases is process_comic: the newest published issue.
func (c *Catalog) comicReleases(ctx context.Context, item Item) ([]release, error) {
	meta, err := c.Reg.Metadata(ctx, item.Kind, item.Source, item.ExternalID)
	if err != nil {
		return nil, err
	}
	last := providers.Int(meta, "max_issue_number")
	if last == 0 || c.Reg.P.ComicVine == nil {
		return nil, nil
	}
	issue, err := c.Reg.P.ComicVine.Issue(ctx, idString(meta["last_issue_id"]))
	if err != nil {
		return nil, err
	}
	text := dateText(issue["store_date"])
	if text == "" {
		text = dateText(issue["cover_date"])
	}
	if text == "" {
		return nil, nil
	}
	at, err := ParseReleaseDate(text)
	if err != nil {
		return nil, nil
	}
	return []release{{number: relNum(last), at: at}}, nil
}

const airingQuery = `
query ($id: Int, $idMal: Int, $page: Int) {
  Media(id: $id, idMal: $idMal, type: ANIME) {
    endDate { year month day }
    episodes
    airingSchedule(page: $page) {
      pageInfo { hasNextPage }
      nodes { episode airingAt }
    }
  }
}`

type airing struct {
	Episode  int    `json:"episode"`
	AiringAt *int64 `json:"airingAt"`
}

// animeReleases is process_anime_bulk for one count-based anime: AniList's
// airing schedule, capped to the episode count, with the final episode added
// at the series end date when AniList lacks it. Without a schedule it falls
// back to the end date like any other title.
func (c *Catalog) animeReleases(ctx context.Context, item Item) ([]release, error) {
	if c.Reg.P.AniList == nil {
		return c.otherReleases(ctx, item)
	}
	schedule, ok, err := c.anilistSchedule(ctx, item)
	if err != nil {
		return nil, err
	}
	if !ok || len(schedule) == 0 {
		return c.otherReleases(ctx, item)
	}
	rows := make([]release, 0, len(schedule))
	for _, a := range schedule {
		if a.AiringAt == nil {
			continue
		}
		rows = append(rows, release{number: relNum(a.Episode), at: time.Unix(*a.AiringAt, 0).UTC()})
	}
	return rows, nil
}

// anilistSchedule ports _process_anilist_media_schedule; ok=false means "not
// usable, fall back".
func (c *Catalog) anilistSchedule(ctx context.Context, item Item) ([]airing, bool, error) {
	id, err := strconv.Atoi(item.ExternalID)
	if err != nil {
		return nil, false, nil
	}
	vars := map[string]any{"idMal": id}
	if item.Source == SourceAniList {
		vars = map[string]any{"id": id}
	}
	var schedule []airing
	var total int
	var end struct{ Year, Month, Day *int }
	for page := 1; page <= 20; page++ {
		vars["page"] = page
		raw, err := c.Reg.P.AniList.GraphQL(ctx, airingQuery, vars)
		if err != nil {
			return nil, false, err
		}
		var resp struct {
			Media *struct {
				EndDate  struct{ Year, Month, Day *int } `json:"endDate"`
				Episodes *int                            `json:"episodes"`
				Airing   struct {
					PageInfo struct {
						HasNextPage bool `json:"hasNextPage"`
					} `json:"pageInfo"`
					Nodes []airing `json:"nodes"`
				} `json:"airingSchedule"`
			} `json:"Media"`
		}
		if err := json.Unmarshal(raw, &resp); err != nil {
			return nil, false, err
		}
		if resp.Media == nil {
			return nil, false, nil
		}
		if resp.Media.Episodes != nil {
			total = *resp.Media.Episodes
		}
		end = resp.Media.EndDate
		schedule = append(schedule, resp.Media.Airing.Nodes...)
		if !resp.Media.Airing.PageInfo.HasNextPage {
			break
		}
	}
	if total == 0 && len(schedule) == 0 {
		return nil, false, nil
	}
	if total > 0 {
		kept := schedule[:0]
		for _, a := range schedule {
			if a.Episode <= total {
				kept = append(kept, a)
			}
		}
		schedule = kept
	}
	incomplete := total == 0 || len(schedule) == 0 || schedule[len(schedule)-1].Episode < total
	if !incomplete {
		return schedule, true, nil
	}
	// AniList can lag behind MAL on the episode count of MAL items.
	if item.Source == SourceMAL && c.Reg.P.MAL != nil {
		if meta, err := c.Reg.P.MAL.Anime(ctx, item.ExternalID); err == nil {
			if malTotal := providers.Int(meta, "max_progress"); malTotal > total && total > 0 {
				return nil, false, nil
			}
		}
	}
	if total == 0 {
		return schedule, true, nil
	}
	if end.Year != nil && *end.Year != 0 {
		month, day := 1, 1
		if end.Month != nil && *end.Month != 0 {
			month = *end.Month
		}
		if end.Day != nil && *end.Day != 0 {
			day = *end.Day
		}
		at := time.Date(*end.Year, time.Month(month), day, sentinelHour, sentinelMinute, sentinelSecond, 0, time.UTC).Unix()
		schedule = append(schedule, airing{Episode: total, AiringAt: &at})
	}
	return schedule, true, nil
}

// UserRelease is a release joined with what the user tracks.
type UserRelease = db.ListReleasesForUserRow

// UserReleases lists the releases between two times (inclusive) of what a
// user is actively tracking, for the calendar feed: Paused and Dropped
// entries are skipped, and so are a show's seasons from its first Paused or
// Dropped season on.
func (c *Catalog) UserReleases(ctx context.Context, userID int64, from, to time.Time) ([]UserRelease, error) {
	return c.Q.ListReleasesForUser(ctx, db.ListReleasesForUserParams{UserID: userID, FromAt: Stamp(from), ToAt: Stamp(to)})
}

// ItemReleases lists the stored releases of an item.
func (c *Catalog) ItemReleases(ctx context.Context, itemID int64) ([]db.Release, error) {
	return c.Q.ListItemReleases(ctx, itemID)
}

// RefreshAllReleases refreshes the releases of every tracked item that needs
// it, like selectors.filter_items_to_fetch: TMDB shows and films only when
// they have no releases yet or TMDB reports changes (a changed show also gets
// its seasons re-synced); comics until their newest issue is a year old;
// everything else while it has no releases or a future one. Returns how many
// items were refreshed; one item failing does not stop the run.
func (c *Catalog) RefreshAllReleases(ctx context.Context) (int, error) {
	items, err := c.Q.ListTrackedItems(ctx)
	if err != nil {
		return 0, err
	}
	var tvChanged, movieChanged map[string]struct{}
	if c.Reg.P.TMDB != nil {
		if tvChanged, err = c.Reg.P.TMDB.TVChanges(ctx); err != nil {
			slog.WarnContext(ctx, "catalog: failed to fetch TMDB TV changes", "err", err)
		}
		if movieChanged, err = c.Reg.P.TMDB.MovieChanges(ctx); err != nil {
			slog.WarnContext(ctx, "catalog: failed to fetch TMDB movie changes", "err", err)
		}
	}
	now := c.now()
	done := 0
	for _, item := range items {
		if ctx.Err() != nil {
			return done, ctx.Err()
		}
		existing, err := c.Q.ListItemReleases(ctx, item.ID)
		if err != nil {
			return done, err
		}
		changed := false
		switch {
		case HasEpisodes(item) && item.Source == SourceTVDB:
			// TVDB has no change feed: a show that is not over is re-synced
			// (at most daily, SyncSeasons throttles) and its releases rebuilt.
			// An ended show is still re-read weekly: shows come back (a new
			// season announced flips TVDB's status back and adds the season).
			changed = showOngoing(item) || refreshedBefore(item, now.Add(-EndedShowRecheck))
		case HasEpisodes(item):
			_, changed = tvChanged[item.ExternalID]
		case IsMovie(item.Kind) && item.Source == SourceTMDB:
			_, changed = movieChanged[item.ExternalID]
		}
		need := len(existing) == 0 || changed
		if !need && !(HasEpisodes(item) || (IsMovie(item.Kind) && item.Source == SourceTMDB)) {
			if item.Kind == KindComic {
				need = latestRelease(existing).After(now.AddDate(-1, 0, 0))
			} else {
				need = latestRelease(existing).After(now)
			}
		}
		if !need {
			continue
		}
		if changed && HasEpisodes(item) {
			opts := []SyncOption{Force}
			if item.Source == SourceTVDB {
				opts = nil
				if fresh, err := c.RefreshItem(ctx, item); err == nil {
					item = fresh // status, poster and title follow TVDB
				}
			}
			if err := c.SyncSeasons(ctx, item, opts...); err != nil {
				slog.WarnContext(ctx, "catalog: season sync failed", "item", item.ID, "err", err)
			}
		}
		if err := c.RefreshReleases(ctx, item); err != nil {
			slog.WarnContext(ctx, "catalog: release refresh failed", "item", item.ID, "title", item.Title, "err", err)
			continue
		}
		done++
	}
	return done, nil
}

func latestRelease(rows []db.Release) time.Time {
	var latest time.Time
	for _, r := range rows {
		if t, err := time.Parse(time.RFC3339, r.At); err == nil && t.After(latest) {
			latest = t
		}
	}
	return latest
}

// showOngoing reports whether a show may still get episodes: its stored TVDB
// status is anything but Ended.
// EndedShowRecheck is how often a show TVDB lists as ended is re-read.
const EndedShowRecheck = 7 * 24 * time.Hour

// refreshedBefore reports whether the item's metadata is older than t (or was
// never refreshed).
func refreshedBefore(item Item, t time.Time) bool {
	if !item.RefreshedAt.Valid {
		return true
	}
	at, err := time.Parse(time.RFC3339, item.RefreshedAt.String)
	return err != nil || at.Before(t)
}

func showOngoing(item Item) bool {
	var meta struct {
		Details struct {
			Status string `json:"status"`
		} `json:"details"`
	}
	if json.Unmarshal([]byte(item.Meta), &meta) != nil {
		return true
	}
	return !strings.EqualFold(meta.Details.Status, "Ended")
}
