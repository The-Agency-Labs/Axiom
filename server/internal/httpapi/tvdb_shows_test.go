package httpapi

import (
	"testing"

	"github.com/the-agency-labs/encompass/server/internal/catalog/catalogtest"
	"github.com/the-agency-labs/encompass/server/internal/library"
)

// useTVDB switches the fixture's catalog to TVDB shows.
func (le *libraryEnv) useTVDB() {
	le.env.UseTVDB(le.env.TVDB)
	le.srv.Catalog = le.env.Catalog
	le.srv.Library = library.New(le.env.Conn, le.env.Q, le.env.Catalog)
	le.srv.Library.Now = le.env.Now
}

func TestTVDBShowEndpointsAndOldTMDBLinks(t *testing.T) {
	le := newLibraryEnv(t)
	le.useTVDB()
	eps := func(base int, n int) []catalogtest.Episode {
		out := catalogtest.Eps(1, n, "2020-01-01")
		for i := range out {
			out[i].ID = int64(base + out[i].Number)
		}
		return out
	}
	le.env.TVDB.AddShow("7000", "Friends",
		catalogtest.Season{Number: 1, AirDate: "2020-01-01", Episodes: eps(71000, 3)},
		catalogtest.Season{Number: 2, AirDate: "2021-01-01", Episodes: eps(72000, 2)},
	).SetIDs("7000", "1668", "tt1")
	le.env.TMDB.AddShow("1668", "Friends (TMDB)", catalogtest.Season{Number: 1, Episodes: catalogtest.Eps(1, 9, "2020-01-01")})

	code, body := le.get("/api/v1/shows/tvdb/7000")
	show := le.expect(code, 200, body, "show")
	if show["source"] != "tvdb" || show["media_id"] != "7000" || show["title"] != "Friends" || len(libraryList(show["seasons"])) != 2 {
		t.Fatalf("show = %v", show)
	}
	// An old link by TMDB id lands on the same series, and says so.
	code, body = le.get("/api/v1/shows/tmdb/1668")
	old := le.expect(code, 200, body, "old url")
	if old["source"] != "tvdb" || old["media_id"] != "7000" || len(libraryList(old["seasons"])) != 2 {
		t.Fatalf("old url = %v", old)
	}
	code, body = le.get("/api/v1/metadata/tv/tmdb/1668")
	md := le.expect(code, 200, body, "old metadata url")
	if md["source"] != "tvdb" || md["media_id"] != "7000" || md["title"] != "Friends" {
		t.Fatalf("metadata = %v", md)
	}
	// Marking an episode through the old URL tracks the TVDB show.
	code, body = le.post("/api/v1/shows/tmdb/1668/seasons/2/episodes", map[string]any{"episodes": []int{1}})
	le.expect(code, 200, body, "mark episode")
	code, body = le.get("/api/v1/library/find?media_type=tv&source=tmdb&media_id=1668")
	found := le.expect(code, 200, body, "find by tmdb id")
	if found["id"] == nil {
		t.Fatalf("find by TMDB id: %v", found)
	}
	code, body = le.get("/api/v1/library/find?media_type=tv&source=tvdb&media_id=7000")
	if again := le.expect(code, 200, body, "find by tvdb id"); again["id"] != found["id"] {
		t.Fatalf("find by tvdb id %v != %v", again, found)
	}
	var tmdbItems int
	if err := le.env.Conn.QueryRow(`SELECT count(*) FROM items WHERE source = 'tmdb'`).Scan(&tmdbItems); err != nil || tmdbItems != 0 {
		t.Errorf("TMDB items made for a show: %d %v", tmdbItems, err)
	}
	// Season detail comes from TVDB.
	code, body = le.get("/api/v1/shows/tvdb/7000/seasons/2")
	season := le.expect(code, 200, body, "season")
	if len(libraryList(season["episodes"])) != 2 || season["source"] != "tvdb" {
		t.Fatalf("season = %v", season)
	}
}
