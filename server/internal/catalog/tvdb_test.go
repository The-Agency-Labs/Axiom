package catalog_test

import (
	"fmt"
	"testing"

	"github.com/the-agency-labs/axiom/server/internal/catalog"
	"github.com/the-agency-labs/axiom/server/internal/catalog/catalogtest"
	"github.com/the-agency-labs/axiom/server/internal/db"
	"github.com/the-agency-labs/axiom/server/internal/providers"
)

func tvdbEnv(t *testing.T) *catalogtest.Env {
	t.Helper()
	env := catalogtest.NewEnv(t, t0)
	env.UseTVDB(env.TVDB)
	return env
}

func tvdbEps(first, n int, date string, idBase int) []catalogtest.Episode {
	eps := catalogtest.Eps(first, n, date)
	for i := range eps {
		eps[i].ID = int64(idBase + eps[i].Number)
	}
	return eps
}

func TestTVDBShowIsStoredWithItsAiredSeasons(t *testing.T) {
	env := tvdbEnv(t)
	env.TVDB.AddShow("7000", "Friends",
		catalogtest.Season{Number: 0, AirDate: "2019-12-01", Episodes: tvdbEps(1, 2, "2019-12-01", 70000)},
		catalogtest.Season{Number: 1, AirDate: "2020-01-01", Episodes: tvdbEps(1, 12, "2020-01-01", 71000)},
		catalogtest.Season{Number: 2, AirDate: "2021-01-01", Episodes: tvdbEps(1, 10, "2021-01-01", 72000)},
	).SetIDs("7000", "1668", "tt0042")

	item, err := env.Catalog.EnsureItem(ctx, "tv", "tvdb", "7000")
	if err != nil {
		t.Fatal(err)
	}
	eq(t, "kind", item.Kind, "tv")
	eq(t, "source", item.Source, "tvdb")
	eq(t, "title", item.Title, "Friends")
	eq(t, "total (regular episodes)", item.Total.Int64, int64(22))
	if err := env.Catalog.SyncSeasons(ctx, item); err != nil {
		t.Fatal(err)
	}
	seasons, err := env.Q.ListItemSeasons(ctx, item.ID)
	if err != nil || len(seasons) != 3 {
		t.Fatalf("seasons = %v %v", seasons, err)
	}
	eq(t, "S0 title", seasons[0].Title, "Specials")
	eq(t, "S2 count", seasons[2].EpisodeCount, int64(10))
	eps, _ := env.Q.ListSeasonEpisodes(ctx, db.ListSeasonEpisodesParams{ItemID: item.ID, Season: 1})
	if len(eps) != 12 || eps[0].ExtID != "71001" || eps[11].Position != 12 {
		t.Fatalf("S1 episodes = %+v", eps)
	}
	// An episode is found by its TVDB id.
	e, err := env.Q.FindEpisodeByExtID(ctx, "72003")
	if err != nil || e.Season != 2 || e.Number != 3 || e.ItemID != item.ID {
		t.Fatalf("by ext id: %+v %v", e, err)
	}
	// Cross ids only for matching.
	ids, _ := env.Catalog.ItemIDs(ctx, item.ID)
	eq(t, "tmdb cross id", fmt.Sprint(ids["tmdb"]), "[1668]")
	eq(t, "imdb cross id", fmt.Sprint(ids["imdb"]), "[tt0042]")
	eq(t, "tvdb id", fmt.Sprint(ids["tvdb"]), "[7000]")
	if env.TMDB.Calls["TV"] != 0 {
		t.Error("TMDB consulted for a show")
	}
	// A TMDB id asked for later resolves to the TVDB show: nothing new is made.
	again, err := env.Catalog.EnsureItem(ctx, "tv", "tmdb", "1668")
	if err != nil || again.ID != item.ID {
		t.Errorf("EnsureItem(tmdb 1668) = %d, %v; want item %d", again.ID, err, item.ID)
	}
	if !catalog.HasEpisodes(item) {
		t.Error("a TVDB show has episodes")
	}
}

func TestTVDBAnimeIsToldApartByTheMapping(t *testing.T) {
	env := tvdbEnv(t)
	env.TVDB.AddShow("8000", "Some Anime", catalogtest.Season{Number: 1, AirDate: "2020-01-01", Episodes: tvdbEps(1, 12, "2020-01-01", 81000)})
	env.TVDB.AddShow("8001", "Live Action", catalogtest.Season{Number: 1, AirDate: "2020-01-01", Episodes: tvdbEps(1, 8, "2020-01-01", 82000)})
	env.Mappings.AddTVDBShow(8000, 1, 0, 11, 111)
	env.Mappings.AddTVDBShow(8000, 2, 0, 12, 112)

	anime, err := env.Catalog.EnsureItem(ctx, "tv", "tvdb", "8000")
	if err != nil {
		t.Fatal(err)
	}
	eq(t, "classified kind", anime.Kind, "anime")
	ids, _ := env.Catalog.ItemIDs(ctx, anime.ID)
	eq(t, "anilist ids", fmt.Sprint(ids["anilist"]), "[11 12]")
	live, err := env.Catalog.EnsureItem(ctx, "tv", "tvdb", "8001")
	if err != nil {
		t.Fatal(err)
	}
	eq(t, "kind", live.Kind, "tv")
	// Asking for anime on a series the mapping does not know keeps it anime, overridden.
	forced, err := env.Catalog.EnsureItem(ctx, "anime", "tvdb", "8001")
	if err != nil {
		t.Fatal(err)
	}
	if forced.ID != live.ID {
		t.Errorf("same series, two items: %d vs %d", forced.ID, live.ID)
	}
}

func TestSearchSplitsShowsFromTVDBAndMoviesFromTMDB(t *testing.T) {
	env := tvdbEnv(t)
	env.TVDB.AddShow("8000", "Re Zero", catalogtest.Season{Number: 1, Episodes: tvdbEps(1, 1, "2020-01-01", 1)})
	env.TVDB.AddShow("8001", "Re Live", catalogtest.Season{Number: 1, Episodes: tvdbEps(1, 1, "2020-01-01", 2)})
	env.Mappings.AddTVDBShow(8000, 1, 0, 11, 111)
	reg := env.Catalog.Reg

	ids := func(page providers.SearchPage) []string {
		var out []string
		for _, h := range providers.List(page, "results") {
			out = append(out, fmt.Sprintf("%s:%v:%s", h["source"], h["media_id"], h["media_type"]))
		}
		return out
	}
	tv, err := reg.Search(ctx, "tv", "", "re", 1, false)
	if err != nil {
		t.Fatal(err)
	}
	eq(t, "tv results", fmt.Sprint(ids(tv)), "[tvdb:8001:tv]")
	anime, err := reg.Search(ctx, "anime", "", "re", 1, false)
	if err != nil {
		t.Fatal(err)
	}
	eq(t, "anime results", fmt.Sprint(ids(anime)), "[tvdb:8000:anime]")
	if _, err := reg.Search(ctx, "movie", "", "re", 1, false); err != nil {
		t.Fatal(err)
	}
	eq(t, "TMDB movie searches", env.TMDB.Calls["Search"], 1)
	eq(t, "TMDB searched for shows", env.TMDB.Calls["Search"], 1)
}

func TestSearchAnimePlacesAniListHitsOnTVDBAndFilmsOnTMDB(t *testing.T) {
	env := tvdbEnv(t)
	env.Mappings.AddTVDBShow(8000, 1, 0, 11, 111)
	env.Mappings.AddTVDBShow(8000, 2, 0, 12, 112) // a second cour of the same series: one result
	env.Mappings.AddMovie(400, 13, 113)
	env.TVDB.AddShow("8000", "Some Anime", catalogtest.Season{Number: 1, Episodes: tvdbEps(1, 1, "2020-01-01", 1)})
	al := &fakeAniList{searches: providers.SearchPage{"page": 1, "results": []providers.Metadata{
		{"media_id": 11, "source": "anilist", "title": "Some Anime"},
		{"media_id": 12, "source": "anilist", "title": "Some Anime 2"},
		{"media_id": 13, "source": "anilist", "title": "A Film"},
		{"media_id": 14, "source": "anilist", "title": "Unknown"},
	}}}
	reg := catalog.NewRegistry(catalog.Providers{TMDB: env.TMDB, TVDB: env.TVDB, AniList: al}, env.Mappings)
	res, err := reg.Search(ctx, "anime", "", "some", 1, false)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, h := range providers.List(res, "results") {
		got = append(got, fmt.Sprintf("%s:%v:%s", h["source"], h["media_id"], h["media_type"]))
	}
	eq(t, "results", fmt.Sprint(got), "[tvdb:8000:anime tmdb:400:anime_movie anilist:14:anime]")
}

func TestTVDBShowReleasesAndOngoingRefresh(t *testing.T) {
	env := tvdbEnv(t)
	env.TVDB.AddShow("7000", "Friends",
		catalogtest.Season{Number: 1, AirDate: "2024-05-01", Episodes: []catalogtest.Episode{
			{ID: 1, Number: 1, AirDate: "2024-05-01", Name: "a"}, {ID: 2, Number: 2, AirDate: "2024-07-01", Name: "b"},
		}})
	uid := env.User(t, "owner")
	item, err := env.Catalog.EnsureItem(ctx, "tv", "tvdb", "7000")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := env.Conn.Exec(`INSERT INTO entries (user_id, item_id, run, status, created_at, updated_at) VALUES (?, ?, 1, 'In progress', 'x', 'x')`, uid, item.ID); err != nil {
		t.Fatal(err)
	}
	n, err := env.Catalog.RefreshAllReleases(ctx)
	if err != nil || n != 1 {
		t.Fatalf("refreshed %d, %v", n, err)
	}
	rel, _ := env.Catalog.ItemReleases(ctx, item.ID)
	if len(rel) != 2 {
		t.Fatalf("releases = %+v", rel)
	}
	// A new episode appears on TVDB; the show is not over, so it is picked up
	// (after the daily season sync window).
	env.TVDB.AddShow("7000", "Friends",
		catalogtest.Season{Number: 1, AirDate: "2024-05-01", Episodes: []catalogtest.Episode{
			{ID: 1, Number: 1, AirDate: "2024-05-01", Name: "a"}, {ID: 2, Number: 2, AirDate: "2024-07-01", Name: "b"},
			{ID: 3, Number: 3, AirDate: "2024-07-08", Name: "c"},
		}})
	env.Advance(25 * 60 * 60 * 1e9)
	if _, err := env.Catalog.RefreshAllReleases(ctx); err != nil {
		t.Fatal(err)
	}
	rel, _ = env.Catalog.ItemReleases(ctx, item.ID)
	if len(rel) != 3 {
		t.Fatalf("releases after new episode = %d", len(rel))
	}
	// An ended show is left alone once it has releases.
	env.TVDB.SetStatus("7000", "Ended")
	if _, err := env.Catalog.RefreshItem(ctx, item); err != nil {
		t.Fatal(err)
	}
	env.TVDB.Calls = nil
	env.Advance(48 * 60 * 60 * 1e9)
	if _, err := env.Catalog.RefreshAllReleases(ctx); err != nil {
		t.Fatal(err)
	}
	for _, c := range env.TVDB.Calls {
		if c == "seasons:7000" {
			t.Errorf("an ended show was re-synced: %v", env.TVDB.Calls)
		}
	}

	// ...but it is re-read weekly: the show comes back with a new season
	// (TVDB flips it to Continuing) and Axiom picks both up.
	env.TVDB.AddShow("7000", "Friends",
		catalogtest.Season{Number: 1, AirDate: "2024-05-01", Episodes: []catalogtest.Episode{
			{ID: 1, Number: 1, AirDate: "2024-05-01", Name: "a"}, {ID: 2, Number: 2, AirDate: "2024-07-01", Name: "b"},
			{ID: 3, Number: 3, AirDate: "2024-07-08", Name: "c"},
		}},
		catalogtest.Season{Number: 2, AirDate: "2026-01-01", Episodes: []catalogtest.Episode{{ID: 4, Number: 1, AirDate: "2026-01-01", Name: "d"}}})
	env.TVDB.SetStatus("7000", "Continuing")
	env.Advance(6 * 24 * 60 * 60 * 1e9) // 8 days since the last refresh
	if _, err := env.Catalog.RefreshAllReleases(ctx); err != nil {
		t.Fatal(err)
	}
	fresh, _ := env.Catalog.GetItem(ctx, item.ID)
	if catalog.ReleaseStatusOfMeta(fresh.Kind, fresh.Meta, env.Now()) != catalog.ReleaseOngoing {
		t.Errorf("status not refreshed: %s", fresh.Meta)
	}
	if rel, _ = env.Catalog.ItemReleases(ctx, item.ID); len(rel) != 4 {
		t.Errorf("releases after the show came back = %d, want 4", len(rel))
	}
}
