package tmdb

import (
	"encoding/json"
	"fmt"
	"strconv"
)

// imgNone is the placeholder image (settings.IMG_NONE in the Django app).
const imgNone = "https://www.themoviedb.org/assets/2/v4/glyphicons/basic/glyphicons-basic-38-picture-grey-c2ebdbb057f2a7614185931650f8cee23fa137b93812ccb132b9df511df1cfac.svg"

type obj = map[string]any

func asObj(v any) obj {
	m, _ := v.(map[string]any)
	return m
}

func asList(v any) []any {
	l, _ := v.([]any)
	return l
}

func num(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	}
	return 0, false
}

// idString renders a JSON id the way Python's f-string would.
func idString(v any) string {
	if n, ok := num(v); ok {
		return strconv.FormatInt(int64(n), 10)
	}
	return fmt.Sprint(v)
}

// normalize round-trips v through JSON so values built here have the same
// types as values read back from the cache.
func normalize(v any) any {
	data, err := json.Marshal(v)
	if err != nil {
		return v
	}
	var out any
	if err := json.Unmarshal(data, &out); err != nil {
		return v
	}
	return out
}

// GetImageURL returns the image URL for a poster/profile/still path (get_image_url).
func GetImageURL(path any) string {
	if s, ok := path.(string); ok && s != "" {
		return "https://image.tmdb.org/t/p/w500" + s
	}
	return imgNone
}

// backdropURL is a title's wide background art for the title page banner, or
// nil when TMDB has none.
func backdropURL(path any) any {
	if s, ok := path.(string); ok && s != "" {
		return "https://image.tmdb.org/t/p/w1280" + s
	}
	return nil
}

func getTitle(m obj) any {
	if t, ok := m["title"]; ok {
		return t
	}
	return m["name"]
}

func getReleaseYear(m obj) any {
	date, _ := m["release_date"].(string)
	if date == "" {
		date, _ = m["first_air_date"].(string)
	}
	if len(date) < 4 {
		return nil
	}
	y, err := strconv.Atoi(date[:4])
	if err != nil {
		return nil
	}
	return y
}

func getStartDate(date any) any {
	if s, ok := date.(string); ok && s == "" {
		return nil
	}
	return date
}

func getEndDate(season obj) any {
	eps := asList(season["episodes"])
	if len(eps) > 0 {
		return asObj(eps[len(eps)-1])["air_date"]
	}
	return nil
}

func getSynopsis(text any) any {
	if s, ok := text.(string); ok && s == "" {
		return "No synopsis available."
	}
	return text
}

// getReadableDuration converts minutes to "1h 5m" / "5m"; nil when zero/unknown.
func getReadableDuration(d any) any {
	n, ok := num(d)
	if !ok || n == 0 {
		return nil
	}
	total := int(n)
	hours, minutes := total/60, total%60
	if hours > 0 {
		return fmt.Sprintf("%dh %dm", hours, minutes)
	}
	return fmt.Sprintf("%dm", minutes)
}

func getRuntimeTV(runtime any) any {
	l := asList(runtime)
	if len(l) > 0 {
		return getReadableDuration(l[0])
	}
	return nil
}

func pluck(list any, key string, limit int) any {
	l := asList(list)
	if len(l) == 0 {
		return nil
	}
	if limit > 0 && len(l) > limit {
		l = l[:limit]
	}
	out := make([]any, 0, len(l))
	for _, e := range l {
		out = append(out, asObj(e)[key])
	}
	return out
}

func getGenres(genres any) any { return pluck(genres, "name", 0) }

func getCountry(countries any) any {
	l := asList(countries)
	if len(l) == 0 {
		return nil
	}
	return asObj(l[0])["name"]
}

func getLanguages(languages any) any { return pluck(languages, "english_name", 0) }

func getCompanies(companies any) any { return pluck(companies, "name", 3) }

// getScore rounds to one decimal place (correctly rounded, like Python's round).
func getScore(score any) float64 {
	n, _ := num(score)
	r, _ := strconv.ParseFloat(strconv.FormatFloat(n, 'f', 1, 64), 64)
	return r
}

// truthy returns the string form of v, or "" when Python would call it falsy.
func truthy(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case float64:
		if x == 0 {
			return ""
		}
		return strconv.FormatInt(int64(x), 10)
	case bool:
		if !x {
			return ""
		}
		return "True"
	}
	return fmt.Sprint(v)
}

// getExternalLinks builds the links map from a TMDB external_ids response
// (get_external_links). tmdbID is only passed for movies.
func getExternalLinks(ids obj, tmdbID string) obj {
	links := obj{}
	if v := truthy(ids["imdb_id"]); v != "" {
		links["IMDb"] = "https://www.imdb.com/title/" + v + "/"
	}
	if v := truthy(ids["tvdb_id"]); v != "" {
		links["TVDB"] = "https://www.thetvdb.com/dereferrer/series/" + v
	}
	if v := truthy(ids["wikidata_id"]); v != "" {
		links["Wikidata"] = "https://www.wikidata.org/wiki/" + v
	}
	if tmdbID != "" {
		links["Letterboxd"] = "https://www.letterboxd.com/tmdb/" + tmdbID
	}
	return links
}

// getRelated formats related media; parent is the show response for seasons.
func getRelated(medias any, mediaType string, parent obj) []any {
	related := []any{}
	for _, e := range asList(medias) {
		media := asObj(e)
		poster := media["poster_path"]
		if mediaType == "season" && (poster == nil || poster == "") {
			poster = parent["poster_path"]
		}
		data := obj{
			"source":     "tmdb",
			"media_type": mediaType,
			"image":      GetImageURL(poster),
		}
		if mediaType == "season" {
			data["media_id"] = parent["id"]
			data["title"] = parent["name"]
			data["season_number"] = media["season_number"]
			data["season_title"] = media["name"]
			data["first_air_date"] = getStartDate(media["air_date"])
			data["max_progress"] = media["episode_count"]
		} else {
			data["media_id"] = media["id"]
			data["title"] = getTitle(media)
		}
		related = append(related, data)
	}
	return related
}
