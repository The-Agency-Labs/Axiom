package anilist

import (
	"sort"
	"time"
)

// Slot is a season and a position (1 = first) inside it.
type Slot struct{ Season, Position int }

// Start is where an AniList entry begins in its TMDB show.
type Start struct{ Season, Offset int }

// startOf is the TMDB (season, offset) where an entry starts. The mapping
// leaves the season out for long runners that are a single AniList entry
// (Naruto, Hunter x Hunter); those start at season 1. ok is false when the
// start is unknown.
func startOf(season *int, offset int, format string) (Start, bool) {
	if season != nil {
		return Start{*season, offset}, true
	}
	switch format {
	case "TV", "TV_SHORT", "ONA":
		return Start{1, 0}, true
	}
	return Start{}, false
}

// episodeSlots lists the (season, position) pairs of `count` episodes from
// start. seasons maps TMDB season numbers to their episode counts. Long
// runners are one AniList entry but several TMDB seasons, so episodes past
// the end of a season continue in the next one, unless another AniList entry
// is mapped to that season (claimed). Specials (season 0) never spill over.
func episodeSlots(start Start, seasons map[int]int, count int, claimed map[int]bool) []Slot {
	var slots []Slot
	episode := start.Offset + 1
	order := []int{start.Season}
	if start.Season != 0 {
		var later []int
		for n := range seasons {
			if n > start.Season {
				later = append(later, n)
			}
		}
		sort.Ints(later)
		for _, n := range later {
			if claimed[n] {
				break
			}
			order = append(order, n)
		}
	}
	for _, n := range order {
		length := seasons[n]
		for len(slots) < count && (length == 0 || episode <= length) {
			slots = append(slots, Slot{n, episode})
			episode++
		}
		if len(slots) >= count {
			break
		}
		episode = 1
	}
	return slots
}

// Dates and the rules of the module doc in anilist_shows.py.
const (
	// BulkDateLimit: a completion date shared by more entries than this is a
	// date the list was filled in on, not a watch date.
	BulkDateLimit = 2
	// AiredSinceYear: series that started in this year or later were watched
	// as they aired.
	AiredSinceYear = 2012
)

// OldSeriesDate is the watch date of older series.
var OldSeriesDate = time.Date(2019, 1, 1, 0, 0, 0, 0, time.UTC)

type fuzzyDate struct {
	Year  int `json:"year"`
	Month int `json:"month"`
	Day   int `json:"day"`
}

type dateKey [3]int

func (d fuzzyDate) key() dateKey { return dateKey{d.Year, d.Month, d.Day} }

// day is the date (UTC midnight), the zero time when unknown; a missing month
// or day counts as 1.
func (d fuzzyDate) day() time.Time {
	if d.Year == 0 {
		return time.Time{}
	}
	m, dd := d.Month, d.Day
	if m == 0 {
		m = 1
	}
	if dd == 0 {
		dd = 1
	}
	return time.Date(d.Year, time.Month(m), dd, 0, 0, 0, 0, time.UTC)
}

// midnight is the date at 00:00 in loc (how list dates are stored), nil when unknown.
func (d fuzzyDate) midnight(loc *time.Location) *time.Time {
	day := d.day()
	if day.IsZero() {
		return nil
	}
	t := time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, loc).UTC()
	return &t
}

// watchDate is when an episode (aired on air, zero = unknown) was watched.
func (r *run) watchDate(e entry, air time.Time) time.Time {
	if e.Status != "COMPLETED" {
		if e.UpdatedAt != 0 {
			return time.Unix(e.UpdatedAt, 0).UTC()
		}
		return r.now
	}
	var day time.Time
	if real := r.realCompletion(e); !real.IsZero() {
		day = real
	} else if e.Media.StartDate.Year >= AiredSinceYear {
		switch {
		case !air.IsZero():
			day = air
		case !e.Media.EndDate.day().IsZero():
			day = e.Media.EndDate.day()
		default:
			day = OldSeriesDate
		}
	} else {
		day = OldSeriesDate
	}
	noon := time.Date(day.Year(), day.Month(), day.Day(), 12, 0, 0, 0, r.loc).UTC()
	if noon.After(r.now) {
		return r.now
	}
	return noon
}

// realCompletion is the entry's completion date unless it is a list fill-in date.
func (r *run) realCompletion(e entry) time.Time {
	c := e.CompletedAt
	if c.Year == 0 || r.bulk[c.key()] {
		return time.Time{}
	}
	return c.day()
}

func parseDay(s string) time.Time {
	if len(s) < 10 {
		return time.Time{}
	}
	t, err := time.Parse(time.DateOnly, s[:10])
	if err != nil {
		return time.Time{}
	}
	return t
}
