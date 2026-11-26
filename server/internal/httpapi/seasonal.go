package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/the-agency-labs/encompass/server/internal/auth"
	"github.com/the-agency-labs/encompass/server/internal/catalog"
)

// Seasonal anime: what aired (or is announced to air) in a Winter/Spring/
// Summer/Fall of a year, read from AniList, which files every anime under a
// season. Titles open through their MyAnimeList id when they have one.

var seasonNames = []string{"WINTER", "SPRING", "SUMMER", "FALL"}

const (
	seasonalYearsBack    = 3
	seasonalYearsForward = 3
	// seasonalMaxYearsAhead stops a stray far-future placeholder from stretching the list.
	seasonalMaxYearsAhead = 10
	seasonalPages         = 4 // 4 x 50 titles per season
	seasonalTTL           = 6 * time.Hour
)

// seasonOf is the season (index into seasonNames) of a month.
func seasonOfMonth(m time.Month) int { return (int(m) - 1) / 3 }

type seasonRef struct {
	Season string `json:"season"` // "winter" ... "fall"
	Year   int    `json:"year"`
	Label  string `json:"label"` // "Winter 2027"
}

func newSeasonRef(index, year int) seasonRef {
	name := strings.ToLower(seasonNames[index])
	return seasonRef{Season: name, Year: year, Label: strings.ToUpper(name[:1]) + name[1:] + " " + strconv.Itoa(year)}
}

// seasonsBetween lists the seasons from (y0, s0) to (y1, s1) inclusive, oldest first.
func seasonsBetween(y0, s0, y1, s1 int) []seasonRef {
	var out []seasonRef
	for y, s := y0, s0; y < y1 || (y == y1 && s <= s1); {
		out = append(out, newSeasonRef(s, y))
		if s++; s == 4 {
			s, y = 0, y+1
		}
	}
	return out
}

// seasonWindow is the seasons offered: seasonalYearsBack years before the
// current season to seasonalYearsForward after it, extended to lastAnnounced
// (year, season index; zero year = none) when anime is announced further out.
func seasonWindow(now time.Time, lastAnnouncedYear, lastAnnouncedSeason int) []seasonRef {
	y, s := now.Year(), seasonOfMonth(now.Month())
	endY, endS := y+seasonalYearsForward, s
	if lastAnnouncedYear > endY || (lastAnnouncedYear == endY && lastAnnouncedSeason > endS) {
		endY, endS = lastAnnouncedYear, lastAnnouncedSeason
	}
	if maxY := y + seasonalMaxYearsAhead; endY > maxY {
		endY = maxY
	}
	return seasonsBetween(y-seasonalYearsBack, s, endY, endS)
}

const seasonalQuery = `
query ($season: MediaSeason, $year: Int, $page: Int) {
  Page(page: $page, perPage: 50) {
    pageInfo { hasNextPage }
    media(type: ANIME, season: $season, seasonYear: $year, isAdult: false, sort: [POPULARITY_DESC, ID]) {
      id idMal format episodes averageScore
      title { userPreferred english romaji }
      coverImage { large }
      startDate { year month day }
      studios(isMain: true) { nodes { name } }
    }
  }
}`

const announcedQuery = `
query {
  Page(page: 1, perPage: 50) {
    media(type: ANIME, status: NOT_YET_RELEASED, isAdult: false, sort: [START_DATE_DESC]) {
      season seasonYear startDate { year month }
    }
  }
}`

type seasonalGraphQL interface {
	GraphQL(ctx context.Context, query string, variables map[string]any) (json.RawMessage, error)
}

// lastAnnounced finds the furthest season with an announced, unreleased anime.
func lastAnnounced(ctx context.Context, g seasonalGraphQL) (year, season int, err error) {
	raw, err := g.GraphQL(ctx, announcedQuery, nil)
	if err != nil {
		return 0, 0, err
	}
	var resp struct {
		Page struct {
			Media []struct {
				Season     *string `json:"season"`
				SeasonYear *int    `json:"seasonYear"`
				StartDate  struct {
					Year  *int `json:"year"`
					Month *int `json:"month"`
				} `json:"startDate"`
			} `json:"media"`
		} `json:"Page"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return 0, 0, err
	}
	for _, m := range resp.Page.Media {
		y, s := 0, 0
		switch {
		case m.SeasonYear != nil && m.Season != nil:
			y = *m.SeasonYear
			for i, n := range seasonNames {
				if n == *m.Season {
					s = i
				}
			}
		case m.StartDate.Year != nil && m.StartDate.Month != nil && *m.StartDate.Month >= 1 && *m.StartDate.Month <= 12:
			y, s = *m.StartDate.Year, seasonOfMonth(time.Month(*m.StartDate.Month))
		}
		if y > year || (y == year && s > season) {
			year, season = y, s
		}
	}
	return year, season, nil
}

type seasonalTitle struct {
	MediaType string  `json:"media_type"`
	Source    string  `json:"source"`
	MediaID   string  `json:"media_id"`
	Title     string  `json:"title"`
	Image     string  `json:"image"`
	Format    string  `json:"format,omitempty"`
	Episodes  *int    `json:"episodes"`
	Score     *int    `json:"score"`
	Studio    string  `json:"studio,omitempty"`
	StartDate *string `json:"start_date"`
}

// seasonalTitles reads one season's anime, most popular first.
func seasonalTitles(ctx context.Context, g seasonalGraphQL, season string, year int) ([]seasonalTitle, error) {
	out := []seasonalTitle{}
	for page := 1; page <= seasonalPages; page++ {
		raw, err := g.GraphQL(ctx, seasonalQuery, map[string]any{"season": strings.ToUpper(season), "year": year, "page": page})
		if err != nil {
			return nil, err
		}
		var resp struct {
			Page struct {
				PageInfo struct {
					HasNextPage bool `json:"hasNextPage"`
				} `json:"pageInfo"`
				Media []struct {
					ID           int64  `json:"id"`
					IDMal        *int64 `json:"idMal"`
					Format       string `json:"format"`
					Episodes     *int   `json:"episodes"`
					AverageScore *int   `json:"averageScore"`
					Title        struct {
						UserPreferred string `json:"userPreferred"`
						English       string `json:"english"`
						Romaji        string `json:"romaji"`
					} `json:"title"`
					CoverImage struct {
						Large string `json:"large"`
					} `json:"coverImage"`
					StartDate struct {
						Year, Month, Day *int
					} `json:"startDate"`
					Studios struct {
						Nodes []struct {
							Name string `json:"name"`
						} `json:"nodes"`
					} `json:"studios"`
				} `json:"media"`
			} `json:"Page"`
		}
		if err := json.Unmarshal(raw, &resp); err != nil {
			return nil, err
		}
		for _, m := range resp.Page.Media {
			t := seasonalTitle{
				MediaType: catalog.KindAnime, Source: catalog.SourceAniList, MediaID: strconv.FormatInt(m.ID, 10),
				Title: m.Title.UserPreferred, Image: m.CoverImage.Large, Episodes: m.Episodes, Format: m.Format,
			}
			if m.IDMal != nil && *m.IDMal != 0 {
				t.Source, t.MediaID = catalog.SourceMAL, strconv.FormatInt(*m.IDMal, 10)
			}
			if m.Format == "MOVIE" {
				t.MediaType = catalog.KindAnimeMovie
			}
			if m.Title.English != "" {
				t.Title = m.Title.English
			}
			if t.Title == "" {
				t.Title = m.Title.Romaji
			}
			if m.AverageScore != nil && *m.AverageScore > 0 {
				t.Score = m.AverageScore
			}
			if len(m.Studios.Nodes) > 0 {
				t.Studio = m.Studios.Nodes[0].Name
			}
			if d := m.StartDate; d.Year != nil && d.Month != nil && d.Day != nil {
				v := time.Date(*d.Year, time.Month(*d.Month), *d.Day, 0, 0, 0, 0, time.UTC).Format("2006-01-02")
				t.StartDate = &v
			}
			out = append(out, t)
		}
		if !resp.Page.PageInfo.HasNextPage {
			break
		}
	}
	return out, nil
}

func (s *Server) seasonalRoutes(mux *http.ServeMux) {
	// GET /seasonal/seasons: the seasons to browse, oldest first, and the current one.
	mux.Handle("GET /api/v1/seasonal/seasons", auth.Require(JSON(func(w http.ResponseWriter, r *http.Request) (any, error) {
		g := s.Catalog.Reg.P.AniList
		if g == nil {
			return nil, Errorf(http.StatusServiceUnavailable, "AniList is not available")
		}
		var last [2]int
		key := "seasonal:last-announced"
		if ok, _ := s.Cache.Get(r.Context(), key, &last); !ok {
			y, sn, err := lastAnnounced(r.Context(), g)
			if err != nil {
				y, sn = 0, 0 // the default window still works without the extension
			} else {
				last = [2]int{y, sn}
				_ = s.Cache.Set(r.Context(), key, last, seasonalTTL)
			}
		}
		now := s.Library.Now()
		return map[string]any{
			"current": newSeasonRef(seasonOfMonth(now.Month()), now.Year()),
			"seasons": seasonWindow(now, last[0], last[1]),
		}, nil
	})))

	// GET /seasonal?season=winter&year=2027: the anime of one season.
	mux.Handle("GET /api/v1/seasonal", auth.Require(JSON(func(w http.ResponseWriter, r *http.Request) (any, error) {
		g := s.Catalog.Reg.P.AniList
		if g == nil {
			return nil, Errorf(http.StatusServiceUnavailable, "AniList is not available")
		}
		season := strings.ToLower(r.URL.Query().Get("season"))
		year, err := strconv.Atoi(r.URL.Query().Get("year"))
		valid := false
		for _, n := range seasonNames {
			valid = valid || strings.ToLower(n) == season
		}
		now := s.Library.Now()
		if !valid || err != nil || year < now.Year()-seasonalYearsBack-1 || year > now.Year()+seasonalMaxYearsAhead {
			return nil, Errorf(http.StatusUnprocessableEntity, "season must be winter, spring, summer or fall, and year a nearby year")
		}
		key := "seasonal:" + season + ":" + strconv.Itoa(year)
		var titles []seasonalTitle
		if ok, _ := s.Cache.Get(r.Context(), key, &titles); !ok {
			titles, err = seasonalTitles(r.Context(), g, season, year)
			if err != nil {
				return nil, Errorf(http.StatusBadGateway, "AniList could not be reached")
			}
			_ = s.Cache.Set(r.Context(), key, titles, seasonalTTL)
		}
		return map[string]any{"season": newSeasonRef(indexOfSeason(season), year), "results": titles}, nil
	})))
}

func indexOfSeason(name string) int {
	for i, n := range seasonNames {
		if strings.ToLower(n) == name {
			return i
		}
	}
	return 0
}
