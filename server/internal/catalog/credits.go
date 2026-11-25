package catalog

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"sort"
	"strconv"
	"time"

	"github.com/the-agency-labs/encompass/server/internal/config"
	"github.com/the-agency-labs/encompass/server/internal/db"
	"github.com/the-agency-labs/encompass/server/internal/providers"
	"github.com/the-agency-labs/encompass/server/internal/providers/tmdb"
)

// Credit roles stored in credits.role.
const (
	RoleCast  = "cast"
	RoleVoice = "voice"
	RoleCrew  = "crew"
)

const (
	tmdbProfile       = "https://image.tmdb.org/t/p/w185"
	maxCast           = 40
	maxCrew           = 8
	tvCrewMinEpisodes = 3
	creditsStaleAfter = 30 * 24 * time.Hour
	creditsRetryAfter = 24 * time.Hour
	// CreditsBackfillLimit is how many items one backfill run fetches.
	CreditsBackfillLimit = 200
)

var (
	movieCrewJobs = map[string]bool{"Director": true, "Screenplay": true, "Writer": true, "Novel": true, "Original Music Composer": true}
	tvCrewJobs    = map[string]bool{"Director": true, "Original Music Composer": true, "Novel": true}
)

// TMDBCredits fetches the raw credits JSON TMDB serves: aggregate_credits for
// kind "tv" (the cast across every season), credits for "movie".
type TMDBCredits interface {
	Credits(ctx context.Context, kind, id string) (map[string]any, error)
}

// NewTMDBCredits returns a TMDBCredits on the shared provider client.
func NewTMDBCredits(base *providers.Client, cfg config.Config) TMDBCredits {
	lang := cfg.TMDBLang
	if lang == "" {
		lang = "en"
	}
	return &tmdbCredits{base: base, key: cfg.TMDBKey, lang: lang, baseURL: "https://api.themoviedb.org/3"}
}

type tmdbCredits struct {
	base      *providers.Client
	key, lang string
	baseURL   string
}

func (t *tmdbCredits) Credits(ctx context.Context, kind, id string) (map[string]any, error) {
	endpoint := "credits"
	if kind == "tv" {
		endpoint = "aggregate_credits"
	}
	q := url.Values{"api_key": {t.key}, "language": {t.lang}}
	var out map[string]any
	err := t.base.JSON(ctx, providers.Request{
		Provider: tmdb.Label,
		URL:      fmt.Sprintf("%s/%s/%s/%s?%s", t.baseURL, kind, url.PathEscape(id), endpoint, q.Encode()),
	}, &out)
	return out, err
}

// creditRow is a credit before it is stored.
type creditRow struct {
	source, extID, name, native, image   string // the person
	role, character, charImage, charRole string
	language, job                        string
	order                                int
	episodes                             sql.NullInt64
	// keep: the person is an existing record recognised by a shared id, so its
	// name is left alone (TVDB spells names its own way).
	keep bool
}

// SupportsCredits reports whether credits can be fetched for an item: TVDB
// shows (their characters; TMDB for a legacy TMDB show), TMDB films, and
// anime films and count-based anime from AniList voice actors.
func SupportsCredits(item Item) bool {
	switch item.Kind {
	case KindTV:
		return item.Source == SourceTVDB || item.Source == SourceTMDB
	case KindMovie:
		return item.Source == SourceTMDB
	case KindAnime:
		return item.Source == SourceTVDB || item.Source == SourceTMDB || item.Source == SourceMAL || item.Source == SourceAniList
	case KindAnimeMovie:
		return item.Source == SourceTMDB || item.Source == SourceMAL || item.Source == SourceAniList
	}
	return false
}

// FetchCredits fetches and stores the cast and crew of an item (TMDB
// aggregate credits for shows, credits for films, AniList voice actors for
// anime) into people, credits and credit_fetches, unless a fetch younger than
// 30 days exists and force is false. Failures are recorded in
// credit_fetches.error and returned.
func (c *Catalog) FetchCredits(ctx context.Context, item Item, force bool) error {
	if !SupportsCredits(item) {
		return nil
	}
	if f, err := c.Q.GetCreditFetch(ctx, item.ID); err == nil && !force {
		if at, perr := time.Parse(time.RFC3339, f.FetchedAt); perr == nil && c.now().Sub(at) < creditsStaleAfter && f.Error == "" {
			return nil
		}
	}
	rows, err := c.gatherCredits(ctx, item)
	if err != nil {
		slog.WarnContext(ctx, "catalog: credits unavailable", "item", item.ID, "title", item.Title, "err", err)
		msg := err.Error()
		if len(msg) > 255 {
			msg = msg[:255]
		}
		if serr := c.Q.UpsertCreditFetch(ctx, db.UpsertCreditFetchParams{ItemID: item.ID, FetchedAt: Stamp(c.now()), Error: msg}); serr != nil {
			return serr
		}
		return err
	}
	return c.tx(ctx, func(q *db.Queries) error {
		if err := q.DeleteItemCredits(ctx, item.ID); err != nil {
			return err
		}
		for _, r := range rows {
			person := db.UpsertPersonParams{
				Source: r.source, ExternalID: r.extID, Name: r.name, NativeName: r.native, Image: r.image,
			}
			var pid int64
			var err error
			if r.keep {
				pid, err = q.EnsurePerson(ctx, db.EnsurePersonParams(person))
			} else {
				pid, err = q.UpsertPerson(ctx, person)
			}
			if err != nil {
				return err
			}
			if err := q.InsertCredit(ctx, db.InsertCreditParams{
				ItemID: item.ID, PersonID: pid, Role: r.role, Character: truncate(r.character, 255),
				CharacterImage: r.charImage, CharacterRole: r.charRole, Language: truncate(r.language, 20),
				Job: r.job, Ord: int64(r.order), EpisodeCount: r.episodes,
			}); err != nil {
				return err
			}
		}
		return q.UpsertCreditFetch(ctx, db.UpsertCreditFetchParams{
			ItemID: item.ID, FetchedAt: Stamp(c.now()), Count: int64(len(rows)),
		})
	})
}

func (c *Catalog) gatherCredits(ctx context.Context, item Item) ([]creditRow, error) {
	var rows []creditRow
	if item.Source == SourceTVDB {
		// Shows are TVDB only: its characters and people, no TMDB credits and
		// no AniList voice actors mixed in.
		return c.tvdbCreditRows(ctx, item)
	}
	if item.Source == SourceTMDB {
		if c.TMDBCredits == nil {
			return nil, errors.New("TMDB credits are not configured")
		}
		kind := "movie"
		if IsShow(item.Kind) {
			kind = "tv"
		}
		raw, err := c.TMDBCredits.Credits(ctx, kind, item.ExternalID)
		if err != nil {
			return nil, err
		}
		rows = tmdbCreditRows(kind, raw)
	}
	if IsShow(item.Kind) || IsMovie(item.Kind) {
		if item.Kind == KindAnime || item.Kind == KindAnimeMovie {
			voices, err := c.voiceRows(ctx, item, len(rows))
			if err != nil {
				if len(rows) == 0 {
					return nil, err
				}
				slog.WarnContext(ctx, "catalog: voice actors unavailable", "item", item.ID, "err", err)
			}
			rows = append(rows, voices...)
		}
	}
	return rows, nil
}

// voiceRows fetches the AniList voice actors of an anime: by its own id for
// count-based anime, through the mapping for TMDB anime. base shifts the
// order so voice rows follow the TMDB cast.
func (c *Catalog) voiceRows(ctx context.Context, item Item, base int) ([]creditRow, error) {
	if c.Reg.P.AniList == nil {
		return nil, nil
	}
	source, id := item.Source, item.ExternalID
	if item.Source == SourceTMDB {
		m := c.Reg.pickMapping(ctx, item.Kind, item.ExternalID)
		if m == nil || !m.AnilistID.Valid {
			return nil, nil
		}
		source, id = SourceAniList, strconv.FormatInt(m.AnilistID.Int64, 10)
	}
	credits, err := c.Reg.P.AniList.Credits(ctx, source, id)
	if err != nil {
		return nil, err
	}
	rows := make([]creditRow, 0, len(credits))
	for _, v := range credits {
		rows = append(rows, creditRow{
			source: SourceAniList, extID: strconv.Itoa(v.PersonID), name: v.PersonName, native: v.PersonNative, image: v.PersonImage,
			role: RoleVoice, character: v.Character, charImage: v.CharacterImage, charRole: v.CharacterRole,
			language: v.Language, order: base + v.Order,
		})
	}
	return rows, nil
}

// tmdbCreditRows ports _tmdb_credits: the first 40 cast members, and the
// crew in the jobs worth listing (for shows only those with 3+ episodes),
// most episodes first, at most 8 distinct (person, job) pairs.
func tmdbCreditRows(kind string, data map[string]any) []creditRow {
	var rows []creditRow
	for order, e := range asList(data["cast"]) {
		if order >= maxCast {
			break
		}
		cast := asMap(e)
		character := providers.Str(cast, "character")
		if roles := asList(cast["roles"]); len(roles) > 0 {
			var names []string
			for _, r := range roles {
				if ch := providers.Str(asMap(r), "character"); ch != "" {
					names = append(names, ch)
				}
			}
			character = joinSlash(names)
		}
		row := creditRow{
			source: SourceTMDB, extID: idString(cast["id"]), name: providers.Str(cast, "name"),
			image: profile(cast), role: RoleCast, character: character, order: order,
		}
		if n := providers.Int(cast, "total_episode_count"); n > 0 {
			row.episodes = sql.NullInt64{Int64: int64(n), Valid: true}
		}
		rows = append(rows, row)
	}

	allowed := movieCrewJobs
	if kind == "tv" {
		allowed = tvCrewJobs
	}
	type crewJob struct {
		episodes int
		crew     map[string]any
		job      string
	}
	var crew []crewJob
	for _, e := range asList(data["crew"]) {
		m := asMap(e)
		jobs := asList(m["jobs"])
		if len(jobs) == 0 {
			jobs = []any{map[string]any{"job": m["job"]}}
		}
		for _, j := range jobs {
			jm := asMap(j)
			job, episodes := providers.Str(jm, "job"), providers.Int(jm, "episode_count")
			if !allowed[job] {
				continue
			}
			if kind == "tv" && episodes < tvCrewMinEpisodes {
				continue
			}
			crew = append(crew, crewJob{episodes, m, job})
		}
	}
	sort.SliceStable(crew, func(i, j int) bool { return crew[i].episodes > crew[j].episodes })
	seen := map[string]bool{}
	for _, cj := range crew {
		key := idString(cj.crew["id"]) + "|" + cj.job
		if seen[key] || len(seen) >= maxCrew {
			continue
		}
		seen[key] = true
		row := creditRow{
			source: SourceTMDB, extID: idString(cj.crew["id"]), name: providers.Str(cj.crew, "name"),
			image: profile(cj.crew), role: RoleCrew, job: cj.job, order: len(rows),
		}
		if cj.episodes > 0 {
			row.episodes = sql.NullInt64{Int64: int64(cj.episodes), Valid: true}
		}
		rows = append(rows, row)
	}
	return rows
}

func profile(m map[string]any) string {
	if p := providers.Str(m, "profile_path"); p != "" {
		return tmdbProfile + p
	}
	return ""
}

func joinSlash(parts []string) string {
	out := ""
	for i, p := range parts {
		if i > 0 {
			out += " / "
		}
		out += p
	}
	return out
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) > n {
		return string(r[:n])
	}
	return s
}

// BackfillCredits fetches credits for library items that have none yet (or
// whose last attempt failed more than a day ago), at most limit per run, and
// returns how many it fetched. A failing item does not stop the run.
func (c *Catalog) BackfillCredits(ctx context.Context, limit int) (int, error) {
	items, err := c.Q.ListItemsMissingCredits(ctx, db.ListItemsMissingCreditsParams{
		RetryBefore: Stamp(c.now().Add(-creditsRetryAfter)), MaxRows: int64(limit),
	})
	if err != nil {
		return 0, err
	}
	fetched := 0
	for _, item := range items {
		if ctx.Err() != nil {
			return fetched, ctx.Err()
		}
		if err := c.FetchCredits(ctx, item, true); err == nil {
			fetched++
		}
	}
	return fetched, nil
}
