package catalog

import (
	"encoding/json"
	"strings"
	"time"
)

// Release statuses of a title itself (not the user's progress).
const (
	ReleaseOngoing   = "ongoing"   // still airing / publishing
	ReleaseHiatus    = "hiatus"    // on a break
	ReleaseEnded     = "ended"     // finished
	ReleaseCancelled = "cancelled" // stopped before it finished
	ReleaseUpcoming  = "upcoming"  // not out yet
)

// statusWords maps provider status strings (TVDB, TMDB, MAL, AniList,
// MangaUpdates), lower-cased, to a release status.
var statusWords = map[string]string{
	"continuing": ReleaseOngoing, "returning series": ReleaseOngoing, "currently airing": ReleaseOngoing,
	"publishing": ReleaseOngoing, "releasing": ReleaseOngoing, "ongoing": ReleaseOngoing, "airing": ReleaseOngoing,
	"on hiatus": ReleaseHiatus, "hiatus": ReleaseHiatus,
	"ended": ReleaseEnded, "finished": ReleaseEnded, "finished airing": ReleaseEnded, "finished publishing": ReleaseEnded,
	"released": ReleaseEnded, "complete": ReleaseEnded, "completed": ReleaseEnded,
	"canceled": ReleaseCancelled, "cancelled": ReleaseCancelled, "discontinued": ReleaseCancelled, "dropped": ReleaseCancelled,
	"upcoming": ReleaseUpcoming, "not yet aired": ReleaseUpcoming, "not yet released": ReleaseUpcoming,
	"not yet published": ReleaseUpcoming, "planned": ReleaseUpcoming, "in production": ReleaseUpcoming,
	"post production": ReleaseUpcoming, "rumored": ReleaseUpcoming, "pilot": ReleaseUpcoming,
}

// serial kinds keep releasing over time; for the others (movies, books,
// games...) only "upcoming" is worth showing: a released film is just a film.
var serialKinds = map[string]bool{KindTV: true, KindAnime: true, KindManga: true, KindComic: true}

// ReleaseStatus is the release status of a title from its provider details
// ("" when unknown or not worth showing). today decides "upcoming" for kinds
// that only have a release date.
func ReleaseStatus(kind string, details map[string]any, today time.Time) string {
	st := ""
	if s, _ := details["status"].(string); s != "" {
		st = statusWords[strings.ToLower(strings.TrimSpace(s))]
	}
	if st == "" { // MangaUpdates: "311 Chapters (Hiatus) 20 Volumes (Ongoing)"; the first note is the comic's own
		if s, _ := details["status_in_country_of_origin"].(string); s != "" {
			st = muStatus(s)
		}
	}
	if st == "" || (!serialKinds[kind] && st == ReleaseEnded) {
		for _, k := range []string{"release_date", "publish_date", "first_air_date", "start_date"} {
			if d, _ := details[k].(string); len(d) >= 10 {
				if t, err := time.Parse("2006-01-02", d[:10]); err == nil && t.After(today) {
					return ReleaseUpcoming
				}
				break
			}
		}
	}
	if !serialKinds[kind] && st != ReleaseUpcoming {
		return ""
	}
	return st
}

// ReleaseStatusOfMeta is ReleaseStatus for an item's stored meta JSON.
func ReleaseStatusOfMeta(kind, meta string, today time.Time) string {
	var m struct {
		Details map[string]any `json:"details"`
	}
	if meta == "" || json.Unmarshal([]byte(meta), &m) != nil {
		return ""
	}
	return ReleaseStatus(kind, m.Details, today)
}

func muStatus(s string) string {
	for {
		i := strings.Index(s, "(")
		if i < 0 {
			return ""
		}
		j := strings.Index(s[i:], ")")
		if j < 0 {
			return ""
		}
		if st := statusWords[strings.ToLower(strings.TrimSpace(s[i+1:i+j]))]; st != "" {
			return st
		}
		s = s[i+j+1:]
	}
}
