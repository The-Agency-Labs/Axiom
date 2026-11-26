package catalog_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"testing"
	"time"

	"github.com/the-agency-labs/encompass/server/internal/catalog"
	"github.com/the-agency-labs/encompass/server/internal/catalog/catalogtest"
	"github.com/the-agency-labs/encompass/server/internal/db"
	"github.com/the-agency-labs/encompass/server/internal/providers"
	"github.com/the-agency-labs/encompass/server/internal/providers/anilist"
)

var (
	ctx = context.Background()
	t0  = time.Date(2024, 6, 1, 12, 0, 0, 0, time.UTC)
)

func eq[T comparable](t *testing.T, what string, got, want T) {
	t.Helper()
	if got != want {
		t.Errorf("%s = %v, want %v", what, got, want)
	}
}

// fakeAniList implements catalog.AniList.
type fakeAniList struct {
	graphql  string // JSON returned by GraphQL
	media    anilist.EpisodeMedia
	credits  []anilist.VoiceCredit
	searches providers.SearchPage
	calls    []string
}

func (f *fakeAniList) Anime(_ context.Context, id string) (catalog.Metadata, error) {
	f.calls = append(f.calls, "anime:"+id)
	return catalog.Metadata{"title": "AniList " + id, "media_id": id, "max_progress": 12, "image": "i", "details": map[string]any{"start_date": "2020-04-01"}}, nil
}
func (f *fakeAniList) Manga(_ context.Context, id string) (catalog.Metadata, error) {
	f.calls = append(f.calls, "manga:"+id)
	return catalog.Metadata{"title": "AniList manga " + id}, nil
}
func (f *fakeAniList) Search(_ context.Context, mt, q string, page int, adult bool) (catalog.SearchPage, error) {
	f.calls = append(f.calls, "search:"+mt)
	return f.searches, nil
}
func (f *fakeAniList) EpisodeMedia(context.Context, string, string) anilist.EpisodeMedia {
	return f.media
}
func (f *fakeAniList) Credits(_ context.Context, source, id string) ([]anilist.VoiceCredit, error) {
	f.calls = append(f.calls, "credits:"+source+":"+id)
	return f.credits, nil
}
func (f *fakeAniList) GraphQL(context.Context, string, map[string]any) (json.RawMessage, error) {
	return json.RawMessage(f.graphql), nil
}

// named is a fake of every other provider: it returns metadata titled with who was asked.
type named struct{ name string }

func (n named) meta(id string) (catalog.Metadata, error) {
	return catalog.Metadata{"title": n.name + ":" + id, "max_progress": 5}, nil
}
func (n named) page() (catalog.SearchPage, error) {
	return catalog.SearchPage{"results": []providers.Metadata{{"title": n.name}}}, nil
}
func (n named) Anime(_ context.Context, id string) (catalog.Metadata, error) { return n.meta(id) }
func (n named) Manga(_ context.Context, id string) (catalog.Metadata, error) { return n.meta(id) }
func (n named) Book(_ context.Context, id string) (catalog.Metadata, error)  { return n.meta(id) }
func (n named) Comic(_ context.Context, id string) (catalog.Metadata, error) { return n.meta(id) }
func (n named) Game(_ context.Context, id string) (catalog.Metadata, error)  { return n.meta(id) }
func (n named) Boardgame(_ context.Context, id string) (catalog.Metadata, error) {
	return n.meta(id)
}
func (n named) Issue(context.Context, string) (catalog.Metadata, error) { return nil, nil }

// typed adapters: Search has a different signature in each client.
type (
	malFake  struct{ named }
	muFake   struct{ named }
	hcFake   struct{ named }
	olFake   struct{ named }
	cvFake   struct{ named }
	igdbFake struct{ named }
	bggFake  struct{ named }
)

func (f malFake) Search(_ context.Context, mt, q string, p int, adult bool) (catalog.SearchPage, error) {
	return catalog.SearchPage{"results": []providers.Metadata{{"title": "mal:" + mt}}}, nil
}
func (f muFake) Search(_ context.Context, q string, p int, adult bool) (catalog.SearchPage, error) {
	return f.page()
}
func (f hcFake) Search(_ context.Context, q string, p int) (catalog.SearchPage, error) {
	return f.page()
}
func (f olFake) Search(_ context.Context, q string, p int) (catalog.SearchPage, error) {
	return f.page()
}
func (f cvFake) Search(_ context.Context, q string, p int) (catalog.SearchPage, error) {
	return f.page()
}
func (f igdbFake) Search(_ context.Context, q string, p int, adult bool) (catalog.SearchPage, error) {
	return f.page()
}
func (f bggFake) Search(_ context.Context, q string, p int) (catalog.SearchPage, error) {
	return f.page()
}

func TestRegistryDispatch(t *testing.T) {
	env := catalogtest.NewEnv(t, t0)
	env.TMDB.AddShow("1", "TMDB Show", catalogtest.Season{Number: 1, Episodes: catalogtest.Eps(1, 1, "2020-01-01")})
	env.TMDB.AddMovie("2", "TMDB Movie", "2020-01-01")
	al := &fakeAniList{}
	reg := catalog.NewRegistry(catalog.Providers{
		TMDB: env.TMDB, AniList: al, MAL: malFake{named{"mal"}}, MangaUpdates: muFake{named{"mu"}},
		Hardcover: hcFake{named{"hc"}}, OpenLibrary: olFake{named{"ol"}}, ComicVine: cvFake{named{"cv"}},
		IGDB: igdbFake{named{"igdb"}}, BGG: bggFake{named{"bgg"}},
	}, env.Mappings)

	meta := []struct {
		kind, source, id, wantTitle string
	}{
		{"tv", "tmdb", "1", "TMDB Show"},
		{"movie", "tmdb", "2", "TMDB Movie"},
		{"anime", "tmdb", "1", "TMDB Show"},
		{"anime_movie", "tmdb", "2", "TMDB Movie"},
		{"anime", "mal", "9", "mal:9"},
		{"anime", "anilist", "9", "AniList 9"},
		{"anime_movie", "mal", "9", "mal:9"},
		{"manga", "mangaupdates", "9", "mu:9"},
		{"manga", "anilist", "9", "AniList manga 9"},
		{"manga", "mal", "9", "mal:9"},
		{"comic", "comicvine", "9", "cv:9"},
		{"book", "hardcover", "9", "hc:9"},
		{"book", "openlibrary", "9", "ol:9"},
		{"game", "igdb", "9", "igdb:9"},
		{"boardgame", "bgg", "9", "bgg:9"},
	}
	for _, tc := range meta {
		got, err := reg.Metadata(ctx, tc.kind, tc.source, tc.id)
		if err != nil {
			t.Errorf("Metadata(%s,%s): %v", tc.kind, tc.source, err)
			continue
		}
		eq(t, fmt.Sprintf("Metadata(%s,%s) title", tc.kind, tc.source), providers.Str(got, "title"), tc.wantTitle)
	}
	for _, tc := range [][2]string{{"tv", "mal"}, {"movie", "anilist"}, {"book", "igdb"}, {"game", "tmdb"}, {"comic", "tmdb"}, {"nope", "tmdb"}} {
		if _, err := reg.Metadata(ctx, tc[0], tc[1], "1"); !errors.Is(err, catalog.ErrUnsupported) {
			t.Errorf("Metadata(%s,%s) = %v, want ErrUnsupported", tc[0], tc[1], err)
		}
	}
	// A missing provider is unsupported, not a nil pointer.
	if _, err := catalog.NewRegistry(catalog.Providers{}, nil).Metadata(ctx, "tv", "tmdb", "1"); !errors.Is(err, catalog.ErrUnsupported) {
		t.Errorf("no TMDB = %v", err)
	}

	search := []struct{ kind, source, want string }{
		{"manga", "mangaupdates", "mu"},
		{"manga", "mal", "mal:manga"},
		{"manga", "", "mal:manga"},
		{"anime", "mal", "mal:anime"},
		{"book", "openlibrary", "ol"},
		{"book", "", "hc"},
		{"book", "hardcover", "hc"},
		{"comic", "", "cv"},
		{"game", "", "igdb"},
		{"boardgame", "", "bgg"},
	}
	for _, tc := range search {
		page, err := reg.Search(ctx, tc.kind, tc.source, "q", 1, true)
		if err != nil {
			t.Errorf("Search(%s,%s): %v", tc.kind, tc.source, err)
			continue
		}
		eq(t, fmt.Sprintf("Search(%s,%s)", tc.kind, tc.source), providers.Str(providers.List(page, "results")[0], "title"), tc.want)
	}
	if _, err := reg.Search(ctx, "tv", "", "q", 1, true); err != nil {
		t.Errorf("tv search: %v", err)
	}
	if env.TMDB.Calls["Search"] != 1 {
		t.Errorf("TMDB searches = %d", env.TMDB.Calls["Search"])
	}
	if _, err := reg.Search(ctx, "movie", "mal", "q", 1, true); err != nil {
		t.Errorf("movie search ignores the source like Django: %v", err)
	}
	al.calls = nil
	if _, err := reg.Search(ctx, "anime", "anilist", "q", 1, true); err != nil || len(al.calls) != 1 || al.calls[0] != "search:anime" {
		t.Errorf("anilist anime search: %v %v", err, al.calls)
	}
}

func TestAnimeSearchPlacesHitsInTMDB(t *testing.T) {
	env := catalogtest.NewEnv(t, t0)
	env.Mappings.AddShow(100, 1, 0, 11, 21)
	env.Mappings.AddShow(100, 2, 0, 12, 22) // a second cour of the same show
	env.Mappings.AddMovie(300, 13, 23)
	al := &fakeAniList{searches: catalog.SearchPage{"page": 1, "results": []providers.Metadata{
		{"media_id": 11, "source": "anilist", "media_type": "anime", "title": "Cour 1"},
		{"media_id": 12, "source": "anilist", "media_type": "anime", "title": "Cour 2"},
		{"media_id": 13, "source": "anilist", "media_type": "anime", "title": "The Film"},
		{"media_id": 99, "source": "anilist", "media_type": "anime", "title": "Unmapped"},
	}}}
	reg := catalog.NewRegistry(catalog.Providers{AniList: al}, env.Mappings)
	page, err := reg.Search(ctx, "anime", "", "q", 1, true)
	if err != nil {
		t.Fatal(err)
	}
	res := providers.List(page, "results")
	if len(res) != 3 {
		t.Fatalf("results = %v (two cours of one show collapse)", res)
	}
	want := [][3]string{{"tmdb", "100", "anime"}, {"tmdb", "300", "anime_movie"}, {"anilist", "99", "anime"}}
	for i, w := range want {
		got := [3]string{providers.Str(res[i], "source"), idString(res[i]["media_id"]), providers.Str(res[i], "media_type")}
		if got != w {
			t.Errorf("result %d = %v, want %v", i, got, w)
		}
	}
}

func nullInt(n int64) sql.NullInt64 { return sql.NullInt64{Int64: n, Valid: true} }

func idString(v any) string {
	switch n := v.(type) {
	case int64:
		return strconv.FormatInt(n, 10)
	case int:
		return strconv.Itoa(n)
	case string:
		return n
	}
	return fmt.Sprint(v)
}

func TestEnsureItemClassifiesAndStoresIds(t *testing.T) {
	env := catalogtest.NewEnv(t, t0)
	env.TMDB.AddShow("100", "Live Action", catalogtest.Season{Number: 1, Episodes: catalogtest.Eps(1, 2, "2020-01-01")})
	env.TMDB.AddShow("200", "Anime Show", catalogtest.Season{Number: 1, Episodes: catalogtest.Eps(1, 3, "2020-01-01")})
	env.TMDB.AddMovie("300", "Anime Film", "2019-01-01")
	env.Mappings.AddShow(200, 1, 0, 5001, 4001)
	env.Mappings.AddShow(200, 2, 0, 5002, 4002)
	env.Mappings.AddMovie(300, 5003, 4003)
	// AniList supplies the native title and studios of the first cour.
	reg := env.Catalog.Reg
	reg.P.AniList = &fakeAniList{graphql: `{"Media":{"id":5001,"title":{"native":"アニメ","romaji":"Anime"},"studios":{"nodes":[{"name":"Studio X"}]}}}`}

	live, err := env.Catalog.EnsureItem(ctx, "tv", "tmdb", "100")
	if err != nil {
		t.Fatal(err)
	}
	eq(t, "live kind", live.Kind, "tv")
	eq(t, "live title", live.Title, "Live Action")
	eq(t, "live year", live.Year.Int64, int64(2010))
	eq(t, "live total (regular episodes)", live.Total.Int64, int64(2))
	eq(t, "live image", live.Image, "https://img/poster.jpg")
	ids, _ := env.Catalog.ItemIDs(ctx, live.ID)
	eq(t, "tmdb id", ids["tmdb"][0], "100")
	eq(t, "tvdb id", ids["tvdb"][0], "7000")
	eq(t, "imdb id", ids["imdb"][0], "tt0042")
	if len(ids["anilist"]) != 0 {
		t.Errorf("live action got anime ids: %v", ids)
	}

	// Asking for "tv" on an anime show gives anime, enriched.
	anime, err := env.Catalog.EnsureItem(ctx, "tv", "tmdb", "200")
	if err != nil {
		t.Fatal(err)
	}
	eq(t, "anime kind", anime.Kind, "anime")
	eq(t, "anime override", anime.KindOverride, int64(0))
	eq(t, "native title", anime.NativeTitle, "アニメ")
	ids, _ = env.Catalog.ItemIDs(ctx, anime.ID)
	if len(ids["anilist"]) != 2 || len(ids["mal"]) != 2 {
		t.Errorf("anime ids = %v", ids)
	}
	var meta map[string]any
	if err := json.Unmarshal([]byte(anime.Meta), &meta); err != nil {
		t.Fatal(err)
	}
	if studios := fmt.Sprint(meta["details"].(map[string]any)["studios"]); studios != "[Studio X]" {
		t.Errorf("studios = %s", studios)
	}

	film, _ := env.Catalog.EnsureItem(ctx, "movie", "tmdb", "300")
	eq(t, "anime film kind", film.Kind, "anime_movie")
	eq(t, "film total", film.Total.Int64, int64(1))

	// EnsureItem is idempotent and finds the item whatever kind is asked for.
	for _, kind := range []string{"tv", "anime"} {
		again, err := env.Catalog.EnsureItem(ctx, kind, "tmdb", "200")
		if err != nil {
			t.Fatal(err)
		}
		eq(t, "same item for "+kind, again.ID, anime.ID)
	}
	if env.TMDB.Calls["TV"] > 3 {
		t.Errorf("TMDB TV called %d times", env.TMDB.Calls["TV"])
	}

	// Lookups by any known id.
	for _, tc := range []struct{ source, id string }{{"imdb", "tt0042"}, {"tvdb", "7000"}, {"tmdb", "100"}} {
		it, err := env.Catalog.FindByExternalID(ctx, tc.source, tc.id)
		if err != nil {
			t.Errorf("FindByExternalID(%s): %v", tc.source, err)
			continue
		}
		if tc.source != "tvdb" { // both test shows share the fake's tvdb id
			eq(t, "found "+tc.source, it.ID, live.ID)
		}
	}
	it, err := env.Catalog.FindByExternalID(ctx, "anilist", "5002", "anime")
	if err != nil || it.ID != anime.ID {
		t.Errorf("by anilist id: %v %v", it.ID, err)
	}
	if _, err := env.Catalog.FindByExternalID(ctx, "anilist", "5002", "tv"); !errors.Is(err, catalog.ErrNotFound) {
		t.Errorf("wrong kind = %v", err)
	}
	if _, err := env.Catalog.FindByExternalID(ctx, "mal", "nope"); !errors.Is(err, catalog.ErrNotFound) {
		t.Errorf("unknown id = %v", err)
	}
	if _, err := env.Catalog.EnsureItem(ctx, "tv", "tmdb", "9999"); !providers.NotFound(err) {
		t.Errorf("missing show = %v", err)
	}
}

func TestKindOverrideSticks(t *testing.T) {
	env := catalogtest.NewEnv(t, t0)
	env.TMDB.AddShow("100", "Show", catalogtest.Season{Number: 1, Episodes: catalogtest.Eps(1, 2, "2020-01-01")})
	env.TMDB.AddShow("200", "Anime", catalogtest.Season{Number: 1, Episodes: catalogtest.Eps(1, 2, "2020-01-01")})
	env.Mappings.AddShow(200, 1, 0, 5001, 4001)

	// The user puts a mapping-unknown show into Anime: kept and pinned.
	forced, err := env.Catalog.EnsureItem(ctx, "anime", "tmdb", "100")
	if err != nil {
		t.Fatal(err)
	}
	eq(t, "forced kind", forced.Kind, "anime")
	eq(t, "forced override", forced.KindOverride, int64(1))
	again, _ := env.Catalog.EnsureItem(ctx, "tv", "tmdb", "100")
	eq(t, "still anime when asked as tv", again.Kind, "anime")

	// An unpinned item follows the mapping when it changes.
	late, _ := env.Catalog.EnsureItem(ctx, "tv", "tmdb", "100")
	_ = late // pinned (forced anime) above; use a fresh show for the follow-the-mapping case
	env.TMDB.AddShow("300", "Later Anime", catalogtest.Season{Number: 1, Episodes: catalogtest.Eps(1, 1, "2020-01-01")})
	s, _ := env.Catalog.EnsureItem(ctx, "tv", "tmdb", "300")
	eq(t, "before mapping", s.Kind, "tv")
	env.Mappings.AddShow(300, 1, 0, 5300, 4300)
	s, _ = env.Catalog.EnsureItem(ctx, "tv", "tmdb", "300")
	eq(t, "after mapping learns it", s.Kind, "anime")
}

func TestSyncSeasonsPositionsAndRefreshWindow(t *testing.T) {
	env := catalogtest.NewEnv(t, t0)
	// Season 2 numbered continuously from 33, plus specials.
	env.TMDB.AddShow("100", "Long Runner",
		catalogtest.Season{Number: 0, Episodes: catalogtest.Eps(1, 1, "2005-01-01")},
		catalogtest.Season{Number: 1, Episodes: catalogtest.Eps(1, 32, "2007-01-01")},
		catalogtest.Season{Number: 2, Episodes: catalogtest.Eps(33, 3, "2009-01-01")})
	item, err := env.Catalog.EnsureItem(ctx, "tv", "tmdb", "100")
	if err != nil {
		t.Fatal(err)
	}
	if err := env.Catalog.SyncSeasons(ctx, item); err != nil {
		t.Fatal(err)
	}
	seasons, _ := env.Q.ListItemSeasons(ctx, item.ID)
	eq(t, "seasons", len(seasons), 3)
	eq(t, "season 2 episode count", seasons[2].EpisodeCount, int64(3))
	eps, _ := env.Q.ListSeasonEpisodes(ctx, db.ListSeasonEpisodesParams{ItemID: item.ID, Season: 2})
	for i, e := range eps {
		eq(t, "number", e.Number, int64(33+i))
		eq(t, "position", e.Position, int64(i+1))
	}
	eq(t, "still", eps[0].Image, "https://image.tmdb.org/t/p/w500/s.jpg")
	eq(t, "kind", eps[0].Kind, "standard")
	got, _ := env.Catalog.GetItem(ctx, item.ID)
	eq(t, "total counts regular seasons only", got.Total.Int64, int64(35))

	calls := env.TMDB.Calls["TVWithSeasons"]
	env.Advance(23 * time.Hour)
	if err := env.Catalog.SyncSeasons(ctx, got); err != nil {
		t.Fatal(err)
	}
	eq(t, "no refetch inside 24h", env.TMDB.Calls["TVWithSeasons"], calls)
	got, _ = env.Catalog.GetItem(ctx, item.ID)
	if err := env.Catalog.SyncSeasons(ctx, got, catalog.Force); err != nil {
		t.Fatal(err)
	}
	eq(t, "forced refetch", env.TMDB.Calls["TVWithSeasons"], calls+1)
	env.Advance(25 * time.Hour)
	got, _ = env.Catalog.GetItem(ctx, item.ID)
	if err := env.Catalog.SyncSeasons(ctx, got); err != nil {
		t.Fatal(err)
	}
	eq(t, "refetch after 24h", env.TMDB.Calls["TVWithSeasons"], calls+2)

	// Removed seasons disappear; count-based anime and movies are skipped.
	env.TMDB.AddShow("100", "Long Runner", catalogtest.Season{Number: 1, Episodes: catalogtest.Eps(1, 2, "2007-01-01")})
	got, _ = env.Catalog.GetItem(ctx, item.ID)
	if err := env.Catalog.SyncSeasons(ctx, got, catalog.Force); err != nil {
		t.Fatal(err)
	}
	seasons, _ = env.Q.ListItemSeasons(ctx, item.ID)
	eq(t, "seasons after TMDB dropped two", len(seasons), 1)
	movie := env.TMDB.AddMovie("5", "Film", "2020-01-01")
	_ = movie
	m, _ := env.Catalog.EnsureItem(ctx, "movie", "tmdb", "5")
	before := env.TMDB.Calls["TV"]
	if err := env.Catalog.SyncSeasons(ctx, m, catalog.Force); err != nil {
		t.Fatal(err)
	}
	eq(t, "movie sync is a no-op", env.TMDB.Calls["TV"], before)
}

func TestAnimeEpisodesMerge(t *testing.T) {
	env := catalogtest.NewEnv(t, t0)
	// TMDB season 2 numbers its episodes 13..24; this cour starts after offset 12.
	env.TMDB.AddShow("100", "Show",
		catalogtest.Season{Number: 2, Episodes: catalogtest.Eps(13, 12, "2021-01-01")})
	env.Mappings.AddShow(100, 2, 12, 5001, 4001)
	al := &fakeAniList{media: anilist.EpisodeMedia{ID: 5001, Episodes: 12, StreamingEpisodes: []anilist.StreamingEpisode{
		{Title: "Episode 1 - From Crunchyroll", Thumbnail: "cr1.jpg"},
		{Title: "Episode 20 - Beyond", Thumbnail: "cr20.jpg"},
	}}}
	env.Catalog.Reg.P.AniList = al
	item := db.Item{Kind: "anime", Source: "mal", ExternalID: "4001", Total: nullInt(10)}

	eps, info, err := env.Catalog.AnimeEpisodes(ctx, item)
	if err != nil {
		t.Fatal(err)
	}
	eq(t, "count capped by the item total", len(eps), 10)
	eq(t, "first number", eps[0].Number, 1)
	eq(t, "TMDB title wins", eps[0].Title, "Episode 13")
	eq(t, "TMDB image wins", eps[0].Image, "https://image.tmdb.org/t/p/w500/s.jpg")
	eq(t, "air date", eps[0].AirDate, "2021-01-01")
	if eps[0].Runtime == nil || *eps[0].Runtime != 24 {
		t.Errorf("runtime = %v", eps[0].Runtime)
	}
	eq(t, "providers", fmt.Sprint(info.Providers), "[tmdb anilist]")
	if info.TMDB == nil || info.TMDB.TMDBID != 100 || info.TMDB.SeasonNumber != 2 || info.TMDB.EpisodeOffset != 12 {
		t.Errorf("tmdb link = %+v", info.TMDB)
	}
	eq(t, "anilist id", info.AniListID, int64(5001))

	// No TMDB mapping: streaming titles fill what they cover, the rest is numbered.
	env2 := catalogtest.NewEnv(t, t0)
	env2.Catalog.Reg.P.AniList = al
	eps, info, err = env2.Catalog.AnimeEpisodes(ctx, db.Item{Kind: "anime", Source: "anilist", ExternalID: "5001"})
	if err != nil {
		t.Fatal(err)
	}
	eq(t, "count from AniList", len(eps), 12)
	eq(t, "streaming title", eps[0].Title, "From Crunchyroll")
	eq(t, "streaming image", eps[0].Image, "cr1.jpg")
	eq(t, "plain numbering", eps[1].Title, "Episode 2")
	eq(t, "kind default", eps[1].Kind, "standard")
	eq(t, "providers", fmt.Sprint(info.Providers), "[anilist]")

	// Without any count, episodes are the numbers some provider knows.
	al2 := &fakeAniList{media: anilist.EpisodeMedia{StreamingEpisodes: []anilist.StreamingEpisode{{Title: "Episode 3 - X"}, {Title: "Episode 5 - Y"}}}}
	env2.Catalog.Reg.P.AniList = al2
	eps, _, _ = env2.Catalog.AnimeEpisodes(ctx, db.Item{Kind: "anime", Source: "anilist", ExternalID: "1"})
	if len(eps) != 5 {
		t.Errorf("episodes up to the highest known = %d", len(eps))
	}

	if _, _, err := env.Catalog.AnimeEpisodes(ctx, db.Item{Kind: "anime", Source: "tmdb", ExternalID: "100"}); !errors.Is(err, catalog.ErrNotCountBased) {
		t.Errorf("TMDB anime = %v", err)
	}
}
