package catalog_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/the-agency-labs/encompass/server/internal/catalog"
	"github.com/the-agency-labs/encompass/server/internal/catalog/catalogtest"
	"github.com/the-agency-labs/encompass/server/internal/db"
	"github.com/the-agency-labs/encompass/server/internal/providers/anilist"
	"github.com/the-agency-labs/encompass/server/internal/providers/tvdb"
)

type fakeCredits struct {
	data  map[string]map[string]any // "tv:1" -> JSON
	err   error
	calls int
}

func (f *fakeCredits) Credits(_ context.Context, kind, id string) (map[string]any, error) {
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	return f.data[kind+":"+id], nil
}

func tvCredits() map[string]any {
	cast := func(id int, name string, chars ...string) map[string]any {
		var roles []any
		for _, c := range chars {
			roles = append(roles, map[string]any{"character": c, "episode_count": 10})
		}
		return map[string]any{"id": id, "name": name, "profile_path": "/p.jpg", "roles": roles, "total_episode_count": 20}
	}
	crew := func(id int, name string, jobs ...any) map[string]any {
		return map[string]any{"id": id, "name": name, "jobs": jobs}
	}
	job := func(j string, eps int) any { return map[string]any{"job": j, "episode_count": eps} }
	return map[string]any{
		"cast": []any{cast(1, "Lead", "Hero", "Villain"), cast(2, "Support", "Friend")},
		"crew": []any{
			crew(10, "Showrunner", job("Director", 12), job("Writer", 30)), // Writer is not a listed TV job
			crew(11, "Guest Director", job("Director", 2)),                 // fewer than 3 episodes
			crew(12, "Composer", job("Original Music Composer", 20)),
			crew(13, "Cameraman", job("Director of Photography", 40)),
		},
	}
}

func newCreditsEnv(t *testing.T) (*catalogtest.Env, *fakeCredits, *fakeAniList) {
	env := catalogtest.NewEnv(t, t0)
	fc := &fakeCredits{data: map[string]map[string]any{"tv:100": tvCredits(), "tv:200": tvCredits(), "movie:500": {
		"cast": []any{map[string]any{"id": 7, "name": "Actor", "character": "Role"}},
		"crew": []any{map[string]any{"id": 8, "name": "Dir", "job": "Director"}, map[string]any{"id": 9, "name": "Grip", "job": "Grip"}},
	}}}
	env.Catalog.TMDBCredits = fc
	al := &fakeAniList{credits: []anilist.VoiceCredit{
		{PersonID: 55, PersonName: "Seiyuu", PersonNative: "声優", Character: "Hero", CharacterRole: "MAIN", Language: "japanese", Order: 0},
		{PersonID: 56, PersonName: "Dub", Character: "Hero", CharacterRole: "MAIN", Language: "english", Order: 0},
	}}
	env.Catalog.Reg.P.AniList = al
	env.TMDB.AddShow("100", "Live", catalogtest.Season{Number: 1, Episodes: catalogtest.Eps(1, 1, "2020-01-01")})
	env.TMDB.AddShow("200", "Anime", catalogtest.Season{Number: 1, Episodes: catalogtest.Eps(1, 1, "2020-01-01")})
	env.TMDB.AddMovie("500", "Film", "2020-01-01")
	env.Mappings.AddShow(200, 1, 0, 5001, 4001)
	return env, fc, al
}

func creditsOf(t *testing.T, env *catalogtest.Env, item db.Item) []db.ListItemCreditsRow {
	t.Helper()
	rows, err := env.Q.ListItemCredits(ctx, item.ID)
	if err != nil {
		t.Fatal(err)
	}
	return rows
}

func TestFetchCreditsTV(t *testing.T) {
	env, fc, _ := newCreditsEnv(t)
	item, _ := env.Catalog.EnsureItem(ctx, "tv", "tmdb", "100")
	if err := env.Catalog.FetchCredits(ctx, item, false); err != nil {
		t.Fatal(err)
	}
	rows := creditsOf(t, env, item)
	var got []string
	for _, r := range rows {
		got = append(got, fmt.Sprintf("%s/%s/%s/%s", r.Role, r.PersonName, r.Character, r.Job))
	}
	want := []string{
		"cast/Lead/Hero / Villain/", "cast/Support/Friend/",
		"crew/Composer//Original Music Composer", "crew/Showrunner//Director", // most episodes first
	}
	if len(got) != len(want) {
		t.Fatalf("credits = %v, want %v", got, want)
	}
	for i := range want {
		eq(t, "credit", got[i], want[i])
	}
	eq(t, "profile image", rows[0].PersonImage, "https://image.tmdb.org/t/p/w185/p.jpg")
	eq(t, "episode count", rows[0].EpisodeCount.Int64, int64(20))
	f, _ := env.Q.GetCreditFetch(ctx, item.ID)
	eq(t, "fetch count", f.Count, int64(4))

	// Fresh credits aren't fetched again unless forced.
	_ = env.Catalog.FetchCredits(ctx, item, false)
	eq(t, "no refetch", fc.calls, 1)
	env.Advance(31 * 24 * time.Hour)
	_ = env.Catalog.FetchCredits(ctx, item, false)
	eq(t, "refetch after 30 days", fc.calls, 2)
	_ = env.Catalog.FetchCredits(ctx, item, true)
	eq(t, "forced refetch", fc.calls, 3)
	eq(t, "rows are replaced, not appended", len(creditsOf(t, env, item)), 4)
}

func TestFetchCreditsMovieAndAnime(t *testing.T) {
	env, _, al := newCreditsEnv(t)
	film, _ := env.Catalog.EnsureItem(ctx, "movie", "tmdb", "500")
	if err := env.Catalog.FetchCredits(ctx, film, false); err != nil {
		t.Fatal(err)
	}
	rows := creditsOf(t, env, film)
	eq(t, "movie credits (actor + director, no grip)", len(rows), 2)
	eq(t, "movie crew job", rows[1].Job, "Director")

	anime, _ := env.Catalog.EnsureItem(ctx, "tv", "tmdb", "200")
	eq(t, "kind", anime.Kind, "anime")
	if err := env.Catalog.FetchCredits(ctx, anime, false); err != nil {
		t.Fatal(err)
	}
	rows = creditsOf(t, env, anime)
	voices := 0
	for _, r := range rows {
		if r.Role == "voice" {
			voices++
			eq(t, "voice source", r.PersonSource, "anilist")
		}
	}
	eq(t, "TMDB cast + crew + 2 voice actors", len(rows), 6)
	eq(t, "voice actors", voices, 2)
	eq(t, "queried AniList through the mapping", al.calls[len(al.calls)-1], "credits:anilist:5001")
	last := rows[len(rows)-1]
	eq(t, "dub language", last.Language, "english")
	eq(t, "native name", rows[4].PersonNativeName, "声優")

	// Count-based anime: voice actors only, by their own id.
	counted, err := env.Q.InsertItem(ctx, db.InsertItemParams{Kind: "anime", Source: "mal", ExternalID: "4001", Title: "Counted", Meta: "{}"})
	if err != nil {
		t.Fatal(err)
	}
	if err := env.Catalog.FetchCredits(ctx, counted, false); err != nil {
		t.Fatal(err)
	}
	eq(t, "count-based credits", len(creditsOf(t, env, counted)), 2)
	eq(t, "queried by MAL id", al.calls[len(al.calls)-1], "credits:mal:4001")

	// Kinds without credits are ignored.
	manga, _ := env.Q.InsertItem(ctx, db.InsertItemParams{Kind: "manga", Source: "mal", ExternalID: "1", Title: "M", Meta: "{}"})
	if err := env.Catalog.FetchCredits(ctx, manga, false); err != nil {
		t.Fatal(err)
	}
	if _, err := env.Q.GetCreditFetch(ctx, manga.ID); err == nil {
		t.Error("credit_fetches row for a manga")
	}
}

func TestCreditsErrorAndBackfill(t *testing.T) {
	env, fc, _ := newCreditsEnv(t)
	user := env.User(t, "u")
	item, _ := env.Catalog.EnsureItem(ctx, "tv", "tmdb", "100")
	movie, _ := env.Catalog.EnsureItem(ctx, "movie", "tmdb", "500")
	for _, it := range []db.Item{item, movie} {
		if _, err := env.Conn.Exec(`INSERT INTO entries (user_id, item_id, status, created_at, updated_at) VALUES (?, ?, 'Planning', 'x', 'x')`, user, it.ID); err != nil {
			t.Fatal(err)
		}
	}
	fc.err = errors.New("TMDB down")
	n, err := env.Catalog.BackfillCredits(ctx, 10)
	if err != nil || n != 0 {
		t.Fatalf("backfill with failures: n=%d err=%v", n, err)
	}
	f, _ := env.Q.GetCreditFetch(ctx, item.ID)
	eq(t, "error recorded", f.Error, "TMDB down")
	if err := env.Catalog.FetchCredits(ctx, item, false); err == nil {
		t.Error("errored fetch not retried by a normal fetch")
	}

	// A failed item isn't retried within the day, then is.
	fc.err = nil
	calls := fc.calls
	if n, _ := env.Catalog.BackfillCredits(ctx, 10); n != 0 || fc.calls != calls {
		t.Errorf("retried too early: n=%d calls %d->%d", n, calls, fc.calls)
	}
	env.Advance(25 * time.Hour)
	n, _ = env.Catalog.BackfillCredits(ctx, 10)
	eq(t, "retried after a day", n, 2)
	n, _ = env.Catalog.BackfillCredits(ctx, 10)
	eq(t, "nothing missing any more", n, 0)

	// Limit.
	item2, _ := env.Catalog.EnsureItem(ctx, "tv", "tmdb", "200")
	env.Conn.Exec(`INSERT INTO entries (user_id, item_id, status, created_at, updated_at) VALUES (?, ?, 'Planning', 'x', 'x')`, user, item2.ID)
	n, _ = env.Catalog.BackfillCredits(ctx, 0)
	eq(t, "limit 0", n, 0)
}

func TestParseReleaseDateAndReleasedEpisodes(t *testing.T) {
	for in, want := range map[string]string{
		"2020":       "2020-01-01T11:59:59Z",
		"2020-05":    "2020-05-01T11:59:59Z",
		"2020-05-17": "2020-05-17T11:59:59Z",
	} {
		got, err := catalog.ParseReleaseDate(in)
		if err != nil {
			t.Fatal(err)
		}
		eq(t, in, got.Format(time.RFC3339), want)
	}
	if _, err := catalog.ParseReleaseDate("soon"); err == nil {
		t.Error("invalid date accepted")
	}

	ep := func(n int, date string) db.ItemEpisode {
		e := db.ItemEpisode{Number: int64(n), Position: int64(n)}
		if date != "" {
			e.AirDate.String, e.AirDate.Valid = date, true
		}
		return e
	}
	// Undated episodes before one that aired inherit "aired" (Django issue #884); after it they don't.
	eps := []db.ItemEpisode{ep(1, "2024-01-01"), ep(2, ""), ep(3, "2024-02-01"), ep(4, ""), ep(5, "2999-01-01")}
	got := catalog.ReleasedEpisodes(eps, "2024-06-01")
	for n, want := range map[int64]bool{1: true, 2: true, 3: true, 4: false, 5: false} {
		if got[n] != want {
			t.Errorf("episode %d released = %v, want %v", n, got[n], want)
		}
	}
	if len(catalog.ReleasedEpisodes(eps, "2023-01-01")) != 0 {
		t.Error("nothing had aired in 2023")
	}
}

func TestRefreshReleasesAndSelection(t *testing.T) {
	env := catalogtest.NewEnv(t, t0)
	env.TMDB.AddShow("100", "Show",
		catalogtest.Season{Number: 1, Episodes: catalogtest.Eps(1, 2, "2020-01-01")},
		catalogtest.Season{Number: 2, Episodes: []catalogtest.Episode{
			{Number: 1, AirDate: "2024-01-01"}, {Number: 2}, {Number: 3, AirDate: "2024-02-01"}, {Number: 4}, {Number: 5, AirDate: "2999-01-01"}}})
	env.TMDB.AddMovie("500", "Film", "2999-12-25")
	show, _ := env.Catalog.EnsureItem(ctx, "tv", "tmdb", "100")
	film, _ := env.Catalog.EnsureItem(ctx, "movie", "tmdb", "500")

	if err := env.Catalog.RefreshReleases(ctx, show); err != nil {
		t.Fatal(err)
	}
	rows, _ := env.Catalog.ItemReleases(ctx, show.ID)
	// 2 in season 1; season 2: ep1, ep2 (undated before an aired one), ep3 and the future ep5; ep4 is unknown.
	eq(t, "show releases", len(rows), 6)
	for _, r := range rows {
		if r.Season.Int64 == 2 && r.Number.Int64 == 2 {
			eq(t, "undated episode inherits the next aired date", r.At, "2024-02-01T11:59:59Z")
		}
		if r.Season.Int64 == 2 && r.Number.Int64 == 4 {
			t.Error("episode with no date got a release")
		}
	}
	if err := env.Catalog.RefreshReleases(ctx, film); err != nil {
		t.Fatal(err)
	}
	rows, _ = env.Catalog.ItemReleases(ctx, film.ID)
	if len(rows) != 1 || rows[0].Number.Valid || rows[0].At != "2999-12-25T11:59:59Z" {
		t.Errorf("movie releases = %+v", rows)
	}

	// Selection: tracked items with releases are skipped until TMDB reports a change.
	user := env.User(t, "u")
	for _, it := range []db.Item{show, film} {
		env.Conn.Exec(`INSERT INTO entries (user_id, item_id, status, created_at, updated_at) VALUES (?, ?, 'In progress', 'x', 'x')`, user, it.ID)
	}
	calls := env.TMDB.Calls["TVWithSeasons"]
	n, err := env.Catalog.RefreshAllReleases(ctx)
	if err != nil {
		t.Fatal(err)
	}
	eq(t, "nothing to refresh", n, 0)
	eq(t, "no TMDB season calls", env.TMDB.Calls["TVWithSeasons"], calls)
	env.TMDB.TVChangesSet = map[string]struct{}{"100": {}}
	n, _ = env.Catalog.RefreshAllReleases(ctx)
	eq(t, "changed show refreshed", n, 1)
	if env.TMDB.Calls["TVWithSeasons"] != calls+1 {
		t.Errorf("seasons not re-synced for the changed show")
	}
	env.TMDB.TVChangesSet = nil
	env.TMDB.MovieChangesSet = map[string]struct{}{"500": {}}
	n, _ = env.Catalog.RefreshAllReleases(ctx)
	eq(t, "changed movie refreshed", n, 1)

	// Calendar listing: paused entries and later seasons of a dropped one are left out.
	from, to := t0, t0.AddDate(1000, 0, 0)
	list, err := env.Catalog.UserReleases(ctx, user, from, to)
	if err != nil {
		t.Fatal(err)
	}
	eq(t, "upcoming (S2E5 + the film)", len(list), 2)
	var showEntry int64
	env.Conn.QueryRow(`SELECT id FROM entries WHERE item_id = ?`, show.ID).Scan(&showEntry)
	env.Conn.Exec(`INSERT INTO entry_seasons (entry_id, season, status) VALUES (?, 2, 'Dropped')`, showEntry)
	list, _ = env.Catalog.UserReleases(ctx, user, from, to)
	eq(t, "after dropping season 2", len(list), 1)
	env.Conn.Exec(`UPDATE entries SET status = 'Paused' WHERE item_id = ?`, film.ID)
	list, _ = env.Catalog.UserReleases(ctx, user, from, to)
	eq(t, "after pausing the film", len(list), 0)
}

// Anime voice credits take the character's picture from TheTVDB when the
// anime maps to a TVDB series with that character; others keep AniList's.
func TestAnimeCharacterArtFromTVDB(t *testing.T) {
	env, _, al := newCreditsEnv(t)
	env.UseTVDB(env.TVDB)
	env.Catalog.Reg.P.AniList = al // UseTVDB rebuilds the registry
	al.credits = []anilist.VoiceCredit{
		{PersonID: 55, PersonName: "A", Character: "Monkey D. Luffy", CharacterImage: "https://anilist/luffy.png", Language: "japanese", Order: 0},
		{PersonID: 56, PersonName: "B", Character: "Zoro", CharacterImage: "https://anilist/zoro.png", Language: "japanese", Order: 1},
		{PersonID: 57, PersonName: "C", Character: "Nami", Language: "japanese", Order: 2},
	}
	env.Mappings.AddTVDBShow(7000, 1, 0, 6001, 6002)
	env.TVDB.AddCharacters("7000",
		tvdb.Character{PersonID: 1, Person: "X", PersonType: "Actor", Character: "Luffy, Monkey D.", Image: "https://tvdb/luffy.jpg"},
		tvdb.Character{PersonID: 2, Person: "Y", PersonType: "Actor", Character: "Nami", Image: "https://tvdb/nami.jpg"},
	)
	item, err := env.Q.InsertItem(ctx, db.InsertItemParams{Kind: "anime", Source: "mal", ExternalID: "6002", Title: "Pirates", Meta: "{}"})
	if err != nil {
		t.Fatal(err)
	}
	if err := env.Catalog.FetchCredits(ctx, item, false); err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, r := range creditsOf(t, env, item) {
		got[r.Character] = r.CharacterImage
	}
	eq(t, "TVDB wins, name order ignored", got["Monkey D. Luffy"], "https://tvdb/luffy.jpg")
	eq(t, "no TVDB match keeps AniList", got["Zoro"], "https://anilist/zoro.png")
	eq(t, "fills a missing picture", got["Nami"], "https://tvdb/nami.jpg")
}
