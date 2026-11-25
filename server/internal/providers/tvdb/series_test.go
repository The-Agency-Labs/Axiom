package tvdb

import (
	"context"
	"testing"
)

const extendedJSON = `{"data": {
  "id": 305089, "name": "Re：ゼロから始める異世界生活", "overview": "日本語のあらすじ", "image": "https://artworks.thetvdb.com/p.jpg",
  "firstAired": "2016-04-04", "lastAired": "2026-09-30", "status": {"name": "Continuing"},
  "originalCountry": "jpn", "originalLanguage": "jpn", "averageRuntime": 27, "year": "2016",
  "originalNetwork": {"name": "AT-X"}, "genres": [{"name": "Fantasy"}, {"name": "Animation"}],
  "remoteIds": [{"id": "tt5607616", "type": 2, "sourceName": "IMDB"}, {"id": "65942", "type": 12, "sourceName": "TheMovieDB.com"}],
  "seasons": [
    {"number": 1, "type": {"type": "official"}, "image": "https://artworks.thetvdb.com/s1.jpg"},
    {"number": 1, "type": {"type": "dvd"}, "image": "https://artworks.thetvdb.com/dvd1.jpg"}
  ],
  "characters": [
    {"id": 1, "peopleId": 301623, "personName": "Inori Minase", "peopleType": "Actor", "name": "Rem", "sort": 3, "isFeatured": true,
     "image": "/banners/v4/actor/1.jpg", "personImgURL": "https://artworks.thetvdb.com/person.jpg"},
    {"id": 2, "peopleId": 9, "personName": "A Director", "peopleType": "Director", "name": "", "sort": 1}
  ]}}`

const translationJSON = `{"data": {"name": "Re: ZERO, Starting Life in Another World", "overview": "Subaru is summoned."}}`

const episodesJSON = `{"data": {"episodes": [
  {"id": 11, "seasonNumber": 1, "number": 2, "name": "Second", "overview": "o2", "aired": "2016-04-11", "runtime": 25, "image": "/banners/e2.jpg", "absoluteNumber": 2},
  {"id": 10, "seasonNumber": 1, "number": 1, "name": "First", "overview": "o1", "aired": "2016-04-04", "runtime": 50, "image": "/banners/e1.jpg", "absoluteNumber": 1},
  {"id": 12, "seasonNumber": 2, "number": 1, "name": "", "aired": "", "runtime": 0, "finaleType": "season"},
  {"id": 9, "seasonNumber": 0, "number": 1, "name": "Special", "aired": "2016-03-01"}
 ]}, "links": {"next": null}}`

func seriesServer(t *testing.T) *server {
	t.Helper()
	return &server{payload: map[string][]byte{
		"/series/305089/extended":              []byte(extendedJSON),
		"/series/305089/translations/eng":      []byte(translationJSON),
		"/series/305089/episodes/official/eng": []byte(episodesJSON),
	}}
}

func TestSeriesIsEnglishAiredOrderFromTVDBAlone(t *testing.T) {
	s := seriesServer(t)
	c := newClient(t, s, "key")
	meta, err := c.Series(context.Background(), "305089")
	if err != nil {
		t.Fatal(err)
	}
	if meta["title"] != "Re: ZERO, Starting Life in Another World" || meta["native_title"] != "Re：ゼロから始める異世界生活" {
		t.Errorf("title %q native %q", meta["title"], meta["native_title"])
	}
	if meta["synopsis"] != "Subaru is summoned." || meta["source"] != "tvdb" || meta["max_progress"] != 3 {
		t.Errorf("synopsis %q source %v total %v", meta["synopsis"], meta["source"], meta["max_progress"])
	}
	d := meta["details"].(map[string]any)
	if d["status"] != "Continuing" || d["country"] != "Japan" || d["seasons"] != 2 || d["runtime"] != "27m" {
		t.Errorf("details = %v", d)
	}
	if g := meta["genres"].([]any); len(g) != 2 || g[0] != "Fantasy" {
		t.Errorf("genres = %v", g)
	}
	if st := d["studios"].([]any); len(st) != 1 || st[0] != "AT-X" {
		t.Errorf("studios = %v", st)
	}
	remote := meta["remote_ids"].(map[string]any)
	if remote["tmdb"] != "65942" || remote["imdb"] != "tt5607616" {
		t.Errorf("remote ids = %v", remote)
	}
	// Seasons: specials first, only the aired (official) order.
	seasons, err := c.Seasons(context.Background(), "305089")
	if err != nil {
		t.Fatal(err)
	}
	if len(seasons) != 3 || seasons[0].Number != 0 || seasons[1].Number != 1 || seasons[2].Number != 2 {
		t.Fatalf("seasons = %+v", seasons)
	}
	if seasons[1].Image != "https://artworks.thetvdb.com/s1.jpg" || seasons[1].AirDate != "2016-04-04" {
		t.Errorf("S1 = %+v", seasons[1])
	}
	eps := seasons[1].Episodes
	if len(eps) != 2 || eps[0].Number != 1 || eps[0].ID != 10 || eps[0].Image != "https://artworks.thetvdb.com/banners/e1.jpg" || eps[1].Name != "Second" {
		t.Errorf("S1 episodes = %+v", eps)
	}
	if seasons[2].Episodes[0].FinaleType != "season" {
		t.Errorf("finale type lost")
	}
}

func TestSeriesWithoutTranslationKeepsTheOriginalOverview(t *testing.T) {
	s := seriesServer(t)
	s.status = map[string]int{"/series/305089/translations/eng": 404}
	c := newClient(t, s, "key")
	meta, err := c.Series(context.Background(), "305089")
	if err != nil {
		t.Fatal(err)
	}
	if meta["title"] != "Re：ゼロから始める異世界生活" || meta["native_title"] != "" || meta["synopsis"] != "日本語のあらすじ" {
		t.Errorf("fallback = %q / %q / %q", meta["title"], meta["native_title"], meta["synopsis"])
	}
}

func TestMissingSeriesIsANotFound(t *testing.T) {
	c := newClient(t, &server{payload: map[string][]byte{}}, "key")
	if _, err := c.Series(context.Background(), "1"); err == nil {
		t.Fatal("expected an error")
	}
}

func TestCharactersAndPersonRemoteIDs(t *testing.T) {
	s := seriesServer(t)
	s.payload["/people/301623/extended"] = []byte(`{"data": {"remoteIds": [
		{"id": "1325949", "type": 15, "sourceName": "TheMovieDB.com"}, {"id": "nm5667822", "type": 16, "sourceName": "IMDB"}]}}`)
	c := newClient(t, s, "key")
	chars, err := c.Characters(context.Background(), "305089")
	if err != nil || len(chars) != 2 {
		t.Fatalf("characters = %+v %v", chars, err)
	}
	a := chars[0]
	if a.PersonID != 301623 || a.Person != "Inori Minase" || a.PersonType != "Actor" || a.Character != "Rem" ||
		a.Image != "https://artworks.thetvdb.com/banners/v4/actor/1.jpg" || !a.Featured {
		t.Errorf("character = %+v", a)
	}
	ids, err := c.PersonRemoteIDs(context.Background(), "301623")
	if err != nil || ids["tmdb"] != "1325949" || ids["imdb"] != "nm5667822" {
		t.Errorf("ids = %v %v", ids, err)
	}
	// A person TVDB does not have: no ids, no error.
	if ids, err := c.PersonRemoteIDs(context.Background(), "404"); err != nil || len(ids) != 0 {
		t.Errorf("unknown person: %v %v", ids, err)
	}
}

func TestSearchPrefersEnglishAndPagesFromZero(t *testing.T) {
	s := &server{payload: map[string][]byte{"/search": []byte(`{"data": [
		{"tvdb_id": "305089", "name": "Re：ゼロ", "translations": {"eng": "Re: ZERO"}, "image_url": "https://x/p.jpg", "year": "2016", "primary_language": "jpn",
		 "overviews": {"eng": "English overview"}},
		{"tvdb_id": "403294", "name": "The Bear", "year": "2022"},
		{"name": "no id"}
	], "links": {"total_items": 2}}`)}}
	c := newClient(t, s, "key")
	page, err := c.Search(context.Background(), "re zero", 1)
	if err != nil {
		t.Fatal(err)
	}
	results := page["results"].([]any)
	if len(results) != 2 {
		t.Fatalf("results = %v", page["results"])
	}
	first := results[0].(map[string]any)
	if first["media_id"] != "305089" || first["source"] != "tvdb" || first["title"] != "Re: ZERO" || first["synopsis"] != "English overview" || first["release_year"] != "2016" {
		t.Errorf("first = %v", first)
	}
	if page["total_results"] != float64(2) && page["total_results"] != 2 {
		t.Errorf("total = %v", page["total_results"])
	}
}

func TestFindByRemoteID(t *testing.T) {
	s := &server{payload: map[string][]byte{
		"/search/remoteid/tt1": []byte(`{"data": [{"series": {"id": 77}}]}`),
		"/search/remoteid/tt2": []byte(`{"data": [{"episode": {"id": 5, "seriesId": 78, "seasonNumber": 2, "number": 3}}]}`),
	}}
	c := newClient(t, s, "key")
	h, err := c.FindByRemoteID(context.Background(), "tt1")
	if err != nil || h == nil || h.SeriesID != 77 || h.Episode != nil {
		t.Fatalf("series hit = %+v %v", h, err)
	}
	h, err = c.FindByRemoteID(context.Background(), "tt2")
	if err != nil || h == nil || h.SeriesID != 78 || h.Episode == nil || h.Episode.Season != 2 || h.Episode.Number != 3 || h.Episode.ID != 5 {
		t.Fatalf("episode hit = %+v %v", h, err)
	}
	if h, err := c.FindByRemoteID(context.Background(), "tt404"); err != nil || h != nil {
		t.Errorf("miss = %+v %v", h, err)
	}
}
