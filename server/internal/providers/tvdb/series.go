package tvdb

// Shows come from TheTVDB: series metadata, the aired ("official") episode
// order with English titles and overviews, cast, search and remote-id
// lookups. TMDB is not consulted for shows.

import (
	"context"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"github.com/the-agency-labs/encompass/server/internal/providers"
)

const (
	artworkHost      = "https://artworks.thetvdb.com"
	seriesURL        = "https://thetvdb.com/dereferrer/series/"
	personURL        = "https://thetvdb.com/dereferrer/people/"
	extendedPrefix   = "tvdb_extended_v2_" // v2: backdrop
	translationPref  = "tvdb_translation_eng_"
	episodesPrefix   = "tvdb_episodes_official_v1_"
	personPrefix     = "tvdb_person_remote_ids_"
	searchPrefix     = "tvdb_search_v1_"
	remoteSearchPref = "tvdb_remoteid_"
	searchPageSize   = 20
	maxEpisodePages  = 40
)

// Episode is one episode of a series in the aired order.
type Episode struct {
	ID         int64  `json:"id"`
	Season     int    `json:"season"`
	Number     int    `json:"number"`
	Absolute   int    `json:"absolute,omitempty"`
	Name       string `json:"name"`
	Overview   string `json:"overview"`
	Aired      string `json:"aired"`
	Runtime    int    `json:"runtime"`
	Image      string `json:"image"`
	IsMovie    bool   `json:"is_movie,omitempty"`
	FinaleType string `json:"finale_type,omitempty"`
}

// Season is a season of a series with the episodes in it.
type Season struct {
	Number   int
	Image    string
	AirDate  string
	Episodes []Episode
}

// Character is a cast or crew row of a series.
type Character struct {
	ID         int64  `json:"id"`
	PersonID   int64  `json:"person_id"`
	Person     string `json:"person"`
	PersonType string `json:"person_type"` // Actor, Director, Writer, Creator, Guest Star, ...
	Character  string `json:"character"`
	Image      string `json:"image"` // the character
	PersonImg  string `json:"person_image"`
	Sort       int    `json:"sort"`
	Featured   bool   `json:"featured"`
}

type extended struct {
	ID               int64          `json:"id"`
	Name             string         `json:"name"`
	Overview         string         `json:"overview"`
	Image            string         `json:"image"`
	Backdrop         string         `json:"backdrop"` // best background art (type 3), textless preferred
	FirstAired       string         `json:"first_aired"`
	LastAired        string         `json:"last_aired"`
	Status           string         `json:"status"`
	OriginalCountry  string         `json:"original_country"`
	OriginalLanguage string         `json:"original_language"`
	AverageRuntime   int            `json:"average_runtime"`
	Year             string         `json:"year"`
	Network          string         `json:"network"`
	Genres           []string       `json:"genres"`
	IMDb             string         `json:"imdb"`
	TMDB             string         `json:"tmdb"`
	SeasonImages     map[int]string `json:"season_images"`
	Characters       []Character    `json:"characters"`
	Found            bool           `json:"found"`
}

// pickBackground chooses the series background (artwork type 3) to show as
// the title page banner: textless art (no language) first, then English, each
// by TVDB's score.
func pickBackground(arts []map[string]any) string {
	best, bestRank, bestScore := "", -1, -1
	for _, a := range arts {
		if fint(a, "type") != 3 {
			continue
		}
		img := artwork(fstr(a, "image"))
		if img == "" {
			continue
		}
		rank := 0
		switch fstr(a, "language") {
		case "":
			rank = 2
		case "eng":
			rank = 1
		}
		if score := fint(a, "score"); rank > bestRank || (rank == bestRank && score > bestScore) {
			best, bestRank, bestScore = img, rank, score
		}
	}
	return best
}

func (c *Client) get(ctx context.Context, path string, dst any) (bool, error) {
	token, err := c.AccessToken(ctx)
	if err != nil {
		return false, err
	}
	if token == "" {
		return false, &providers.Error{Provider: Label, Details: "TVDB_API is not configured"}
	}
	if err := c.authedGet(ctx, token, path, dst); err != nil {
		if providers.NotFound(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// Configured reports whether a TVDB key is set.
func (c *Client) Configured() bool { return c.cfg.TVDBKey != "" }

func artwork(u string) string {
	switch {
	case u == "":
		return ""
	case strings.HasPrefix(u, "/"):
		return artworkHost + u
	}
	return u
}

func fstr(m map[string]any, k string) string {
	switch v := m[k].(type) {
	case string:
		return v
	case float64:
		return strconv.FormatInt(int64(v), 10)
	}
	return ""
}

func fint(m map[string]any, k string) int {
	switch v := m[k].(type) {
	case float64:
		return int(v)
	case string:
		n, _ := strconv.Atoi(v)
		return n
	}
	return 0
}

func fmap(m map[string]any, k string) map[string]any {
	v, _ := m[k].(map[string]any)
	return v
}

func flist(m map[string]any, k string) []map[string]any {
	l, _ := m[k].([]any)
	out := make([]map[string]any, 0, len(l))
	for _, e := range l {
		if o, ok := e.(map[string]any); ok {
			out = append(out, o)
		}
	}
	return out
}

func (c *Client) extended(ctx context.Context, id string) (extended, error) {
	key := extendedPrefix + id
	var x extended
	if c.cacheGet(ctx, key, &x) && x.Found {
		return x, nil
	}
	var resp struct {
		Data map[string]any `json:"data"`
	}
	ok, err := c.get(ctx, "/series/"+url.PathEscape(id)+"/extended", &resp)
	if err != nil {
		return extended{}, err
	}
	if !ok || resp.Data == nil {
		return extended{}, providers.NotFoundError(Label, "series", id)
	}
	d := resp.Data
	x = extended{
		Found: true, ID: int64(fint(d, "id")), Name: fstr(d, "name"), Overview: fstr(d, "overview"),
		Image: artwork(fstr(d, "image")), FirstAired: fstr(d, "firstAired"), LastAired: fstr(d, "lastAired"),
		Status: fstr(fmap(d, "status"), "name"), OriginalCountry: fstr(d, "originalCountry"),
		OriginalLanguage: fstr(d, "originalLanguage"), AverageRuntime: fint(d, "averageRuntime"), Year: fstr(d, "year"),
		Network: fstr(fmap(d, "originalNetwork"), "name"), SeasonImages: map[int]string{},
	}
	if x.Network == "" {
		x.Network = fstr(fmap(d, "latestNetwork"), "name")
	}
	for _, g := range flist(d, "genres") {
		if n := fstr(g, "name"); n != "" {
			x.Genres = append(x.Genres, n)
		}
	}
	for _, r := range flist(d, "remoteIds") {
		switch int(fint(r, "type")) {
		case 2:
			x.IMDb = fstr(r, "id")
		case 12:
			x.TMDB = fstr(r, "id")
		default:
			switch strings.ToLower(fstr(r, "sourceName")) {
			case "imdb":
				if x.IMDb == "" {
					x.IMDb = fstr(r, "id")
				}
			case "themoviedb.com":
				if x.TMDB == "" {
					x.TMDB = fstr(r, "id")
				}
			}
		}
	}
	x.Backdrop = pickBackground(flist(d, "artworks"))
	for _, s := range flist(d, "seasons") {
		if fstr(fmap(s, "type"), "type") != "official" {
			continue
		}
		if img := artwork(fstr(s, "image")); img != "" {
			x.SeasonImages[fint(s, "number")] = img
		}
	}
	for _, ch := range flist(d, "characters") {
		x.Characters = append(x.Characters, Character{
			ID: int64(fint(ch, "id")), PersonID: int64(fint(ch, "peopleId")), Person: fstr(ch, "personName"),
			PersonType: fstr(ch, "peopleType"), Character: fstr(ch, "name"), Image: artwork(fstr(ch, "image")),
			PersonImg: artwork(fstr(ch, "personImgURL")), Sort: fint(ch, "sort"), Featured: ch["isFeatured"] == true,
		})
	}
	c.cacheSet(ctx, key, x)
	return x, nil
}

// translation is the English name and overview ("" when TVDB has none).
func (c *Client) translation(ctx context.Context, id string) (name, overview string, err error) {
	key := translationPref + id
	var t struct{ Name, Overview string }
	if c.cacheGet(ctx, key, &t) {
		return t.Name, t.Overview, nil
	}
	var resp struct {
		Data map[string]any `json:"data"`
	}
	ok, err := c.get(ctx, "/series/"+url.PathEscape(id)+"/translations/eng", &resp)
	if err != nil {
		return "", "", err
	}
	if ok && resp.Data != nil {
		t.Name, t.Overview = fstr(resp.Data, "name"), fstr(resp.Data, "overview")
	}
	c.cacheSet(ctx, key, t)
	return t.Name, t.Overview, nil
}

// Episodes returns every episode of a series in the aired order, with English
// titles and overviews where TVDB has them, sorted by season and number.
func (c *Client) Episodes(ctx context.Context, id string) ([]Episode, error) {
	key := episodesPrefix + id
	var cached struct {
		Episodes []Episode
		Found    bool
	}
	if c.cacheGet(ctx, key, &cached) && cached.Found {
		return cached.Episodes, nil
	}
	var out []Episode
	for page := 0; page < maxEpisodePages; page++ {
		var resp struct {
			Data  map[string]any `json:"data"`
			Links struct {
				Next any `json:"next"`
			} `json:"links"`
		}
		ok, err := c.get(ctx, fmt.Sprintf("/series/%s/episodes/official/eng?page=%d", url.PathEscape(id), page), &resp)
		if err != nil {
			return nil, err
		}
		if !ok {
			break
		}
		for _, e := range flist(resp.Data, "episodes") {
			out = append(out, Episode{
				ID: int64(fint(e, "id")), Season: fint(e, "seasonNumber"), Number: fint(e, "number"),
				Absolute: fint(e, "absoluteNumber"), Name: strings.TrimSpace(fstr(e, "name")),
				Overview: strings.TrimSpace(fstr(e, "overview")), Aired: fstr(e, "aired"), Runtime: fint(e, "runtime"),
				Image: artwork(fstr(e, "image")), IsMovie: fint(e, "isMovie") == 1, FinaleType: fstr(e, "finaleType"),
			})
		}
		if resp.Links.Next == nil {
			break
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Season != out[j].Season {
			return out[i].Season < out[j].Season
		}
		return out[i].Number < out[j].Number
	})
	cached.Episodes, cached.Found = out, true
	c.cacheSet(ctx, key, cached)
	return out, nil
}

// Seasons groups the episodes of a series into its aired seasons (season 0 =
// specials), each with its poster and first air date. Empty seasons are left out.
func (c *Client) Seasons(ctx context.Context, id string) ([]Season, error) {
	x, err := c.extended(ctx, id)
	if err != nil {
		return nil, err
	}
	eps, err := c.Episodes(ctx, id)
	if err != nil {
		return nil, err
	}
	return groupSeasons(x, eps), nil
}

func groupSeasons(x extended, eps []Episode) []Season {
	var out []Season
	for _, e := range eps {
		if len(out) == 0 || out[len(out)-1].Number != e.Season {
			out = append(out, Season{Number: e.Season, Image: x.SeasonImages[e.Season]})
		}
		s := &out[len(out)-1]
		s.Episodes = append(s.Episodes, e)
		if e.Aired != "" && (s.AirDate == "" || e.Aired < s.AirDate) {
			s.AirDate = e.Aired
		}
	}
	return out
}

func seasonTitle(n int) string {
	if n == 0 {
		return "Specials"
	}
	return fmt.Sprintf("Season %d", n)
}

// Series returns the metadata of a series in the shape the catalog reads
// (the same keys the TMDB client returns for a show): English title and
// synopsis, poster, genres, status, network, year and the aired seasons.
// Cross ids (imdb, tmdb) are under "remote_ids", for matching only.
func (c *Client) Series(ctx context.Context, id string) (providers.Metadata, error) {
	x, err := c.extended(ctx, id)
	if err != nil {
		return nil, err
	}
	name, overview, err := c.translation(ctx, id)
	if err != nil {
		return nil, err
	}
	eps, err := c.Episodes(ctx, id)
	if err != nil {
		return nil, err
	}
	title := firstNonEmpty(name, x.Name)
	native := ""
	if x.Name != "" && x.Name != title && x.OriginalLanguage != "eng" {
		native = x.Name
	}
	// English overview; the original-language one only when TVDB has no translation.
	synopsis := firstNonEmpty(overview, x.Overview, "No synopsis available.")

	seasons := groupSeasons(x, eps)
	related := make([]any, 0, len(seasons))
	regular, regularSeasons := 0, 0
	for _, s := range seasons {
		img := s.Image
		if img == "" {
			img = x.Image
		}
		related = append(related, map[string]any{
			"source": "tvdb", "media_type": "season", "media_id": id, "title": title,
			"season_number": s.Number, "season_title": seasonTitle(s.Number), "image": img,
			"first_air_date": nilIfEmpty(s.AirDate), "max_progress": len(s.Episodes),
		})
		if s.Number != 0 {
			regular += len(s.Episodes)
			regularSeasons++
		}
	}
	genres := make([]any, 0, len(x.Genres))
	for _, g := range x.Genres {
		genres = append(genres, g)
	}
	studios := []any{}
	if x.Network != "" {
		studios = append(studios, x.Network)
	}
	links := map[string]any{"TVDB": seriesURL + id}
	remote := map[string]any{}
	if x.IMDb != "" {
		links["IMDb"] = "https://www.imdb.com/title/" + x.IMDb
		remote["imdb"] = x.IMDb
	}
	if x.TMDB != "" {
		remote["tmdb"] = x.TMDB
	}
	first := firstNonEmpty(x.FirstAired, firstAired(eps))
	return providers.Metadata{
		"media_id": id, "source": "tvdb", "source_url": seriesURL + id, "media_type": "tv",
		"title": title, "native_title": native, "max_progress": regular, "image": x.Image, "backdrop": nilIfEmpty(x.Backdrop),
		"synopsis": synopsis, "genres": genres, "score": nil, "score_count": nil,
		"details": map[string]any{
			"format": "TV", "first_air_date": nilIfEmpty(first), "last_air_date": nilIfEmpty(x.LastAired),
			"status": nilIfEmpty(x.Status), "seasons": regularSeasons, "episodes": regular,
			"runtime": readable(x.AverageRuntime), "studios": studios, "country": nilIfEmpty(countryName(x.OriginalCountry)),
			"languages": languageList(x.OriginalLanguage),
		},
		"related":           map[string]any{"seasons": related, "recommendations": []any{}},
		"tvdb_id":           id,
		"external_links":    links,
		"remote_ids":        remote,
		"original_language": x.OriginalLanguage,
	}, nil
}

func firstAired(eps []Episode) string {
	best := ""
	for _, e := range eps {
		if e.Season != 0 && e.Aired != "" && (best == "" || e.Aired < best) {
			best = e.Aired
		}
	}
	return best
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func nilIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func readable(minutes int) any {
	if minutes <= 0 {
		return nil
	}
	if minutes >= 60 {
		return fmt.Sprintf("%dh %dm", minutes/60, minutes%60)
	}
	return fmt.Sprintf("%dm", minutes)
}

var countries = map[string]string{
	"jpn": "Japan", "usa": "United States", "gbr": "United Kingdom", "kor": "South Korea", "can": "Canada",
	"fra": "France", "deu": "Germany", "chn": "China", "esp": "Spain", "ita": "Italy", "aus": "Australia",
	"swe": "Sweden", "dnk": "Denmark", "nor": "Norway", "nld": "Netherlands", "bra": "Brazil", "mex": "Mexico",
	"ind": "India", "rus": "Russia", "tha": "Thailand", "twn": "Taiwan", "irl": "Ireland", "nzl": "New Zealand",
	"pol": "Poland", "tur": "Turkey", "arg": "Argentina", "bel": "Belgium", "fin": "Finland", "isr": "Israel",
}

var languages = map[string]string{
	"eng": "English", "jpn": "Japanese", "kor": "Korean", "fra": "French", "deu": "German", "spa": "Spanish",
	"ita": "Italian", "zho": "Chinese", "por": "Portuguese", "rus": "Russian", "swe": "Swedish", "dan": "Danish",
	"nor": "Norwegian", "nld": "Dutch", "hin": "Hindi", "tha": "Thai", "pol": "Polish", "tur": "Turkish",
}

func countryName(code string) string {
	if n, ok := countries[strings.ToLower(code)]; ok {
		return n
	}
	return strings.ToUpper(code)
}

func languageList(code string) []any {
	if code == "" {
		return []any{}
	}
	if n, ok := languages[strings.ToLower(code)]; ok {
		return []any{n}
	}
	return []any{strings.ToUpper(code)}
}

// Season returns one season in the shape of the TMDB season metadata the
// season endpoints read: its title, poster, the series synopsis and the
// episodes (episode_number, name, overview, air_date, runtime, image).
func (c *Client) Season(ctx context.Context, id string, number int) (providers.Metadata, error) {
	meta, err := c.Series(ctx, id)
	if err != nil {
		return nil, err
	}
	seasons, err := c.Seasons(ctx, id)
	if err != nil {
		return nil, err
	}
	for _, s := range seasons {
		if s.Number != number {
			continue
		}
		img := firstNonEmpty(s.Image, providers.Str(meta, "image"))
		eps := make([]any, 0, len(s.Episodes))
		for _, e := range s.Episodes {
			eps = append(eps, map[string]any{
				"episode_number": e.Number, "name": e.Name, "overview": e.Overview, "air_date": nilIfEmpty(e.Aired),
				"runtime": e.Runtime, "image": e.Image, "ext_id": strconv.FormatInt(e.ID, 10),
			})
		}
		return providers.Metadata{
			"source": "tvdb", "media_type": "season", "media_id": id, "title": meta["title"],
			"season_number": number, "season_title": seasonTitle(number), "image": img,
			"synopsis": meta["synopsis"], "max_progress": len(s.Episodes), "episodes": eps,
			"details": map[string]any{"first_air_date": nilIfEmpty(s.AirDate), "episodes": len(s.Episodes)},
			"genres":  meta["genres"], "tvdb_id": id, "external_links": meta["external_links"],
		}, nil
	}
	return nil, providers.NotFoundError(Label, "season", fmt.Sprintf("%s/%d", id, number))
}

// Characters returns the cast and crew rows TVDB lists for a series.
func (c *Client) Characters(ctx context.Context, id string) ([]Character, error) {
	x, err := c.extended(ctx, id)
	if err != nil {
		return nil, err
	}
	return x.Characters, nil
}

// PersonURL is the public page of a TVDB person.
func PersonURL(id string) string { return personURL + id }

// PersonRemoteIDs returns the ids TVDB has for a person at other databases
// (keys "tmdb" and "imdb"), which is how a TVDB person is recognised as a
// person we already know. A person TVDB no longer has gives an empty map.
func (c *Client) PersonRemoteIDs(ctx context.Context, personID string) (map[string]string, error) {
	key := personPrefix + personID
	var cached struct {
		IDs   map[string]string
		Found bool
	}
	if c.cacheGet(ctx, key, &cached) && cached.Found {
		return cached.IDs, nil
	}
	var resp struct {
		Data map[string]any `json:"data"`
	}
	ok, err := c.get(ctx, "/people/"+url.PathEscape(personID)+"/extended", &resp)
	if err != nil {
		return nil, err
	}
	ids := map[string]string{}
	if ok {
		for _, r := range flist(resp.Data, "remoteIds") {
			switch strings.ToLower(fstr(r, "sourceName")) {
			case "themoviedb.com":
				ids["tmdb"] = fstr(r, "id")
			case "imdb":
				ids["imdb"] = fstr(r, "id")
			}
		}
	}
	cached.IDs, cached.Found = ids, true
	c.cacheSet(ctx, key, cached)
	return ids, nil
}

// Search searches series (anime and live action) by title. Results carry the
// English title and overview when TVDB has them, the poster and the first
// air year, in the shape of the TMDB search page. Page is 1-based.
func (c *Client) Search(ctx context.Context, query string, page int) (providers.SearchPage, error) {
	if page < 1 {
		page = 1
	}
	key := fmt.Sprintf("%s%s_%d", searchPrefix, strings.ToLower(strings.TrimSpace(query)), page)
	var data providers.SearchPage
	err := c.base.Cached(ctx, key, cacheTTL, &data, func() (any, error) {
		var resp struct {
			Data  []map[string]any `json:"data"`
			Links map[string]any   `json:"links"`
		}
		q := url.Values{"query": {query}, "type": {"series"}, "limit": {strconv.Itoa(searchPageSize)}, "page": {strconv.Itoa(page - 1)}}
		ok, err := c.get(ctx, "/search?"+q.Encode(), &resp)
		if err != nil {
			return nil, err
		}
		results := []providers.Metadata{}
		if ok {
			for _, m := range resp.Data {
				id := fstr(m, "tvdb_id")
				if id == "" {
					continue
				}
				title := fstr(m, "name")
				if tr := fmap(m, "translations"); tr != nil && fstr(tr, "eng") != "" {
					title = fstr(tr, "eng")
				}
				entry := providers.Metadata{
					"media_id": id, "source": "tvdb", "media_type": "tv", "title": title,
					"image": fstr(m, "image_url"), "release_year": nilIfEmpty(fstr(m, "year")),
					"original_language": fstr(m, "primary_language"),
				}
				if o := fmap(m, "overviews"); o != nil && fstr(o, "eng") != "" {
					entry["synopsis"] = fstr(o, "eng")
				}
				results = append(results, entry)
			}
		}
		total := fint(resp.Links, "total_items")
		return providers.SearchPage{
			"page": page, "total_results": total, "total_pages": total/searchPageSize + 1, "results": results,
		}, nil
	})
	return data, err
}

// Hit is a series found by a remote id.
type Hit struct {
	SeriesID int64
	Episode  *Episode // set when the id belongs to one episode of the series
}

// FindByRemoteID resolves an id of another database (an IMDb "tt..." id or a
// TMDB id) to the TVDB series or episode it belongs to. nil when none.
func (c *Client) FindByRemoteID(ctx context.Context, remoteID string) (*Hit, error) {
	key := remoteSearchPref + remoteID
	var cached struct {
		SeriesID  int64
		EpisodeID int64
		Season    int
		Number    int
		Found     bool
	}
	if !c.cacheGet(ctx, key, &cached) {
		var resp struct {
			Data []map[string]any `json:"data"`
		}
		if _, err := c.get(ctx, "/search/remoteid/"+url.PathEscape(remoteID), &resp); err != nil {
			return nil, err
		}
		for _, r := range resp.Data {
			if s := fmap(r, "series"); s != nil {
				cached.SeriesID, cached.Found = int64(fint(s, "id")), true
				break
			}
			if e := fmap(r, "episode"); e != nil {
				cached.SeriesID, cached.EpisodeID = int64(fint(e, "seriesId")), int64(fint(e, "id"))
				cached.Season, cached.Number, cached.Found = fint(e, "seasonNumber"), fint(e, "number"), true
				break
			}
		}
		c.cacheSet(ctx, key, cached)
	}
	if !cached.Found || cached.SeriesID == 0 {
		return nil, nil
	}
	h := &Hit{SeriesID: cached.SeriesID}
	if cached.EpisodeID != 0 {
		h.Episode = &Episode{ID: cached.EpisodeID, Season: cached.Season, Number: cached.Number}
	}
	return h, nil
}
