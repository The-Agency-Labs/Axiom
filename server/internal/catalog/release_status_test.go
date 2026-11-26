package catalog

import (
	"testing"
	"time"
)

func TestReleaseStatus(t *testing.T) {
	today := time.Date(2026, 11, 25, 0, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		kind    string
		details map[string]any
		want    string
	}{
		{KindTV, map[string]any{"status": "Continuing"}, ReleaseOngoing},
		{KindAnime, map[string]any{"status": "Ended"}, ReleaseEnded},
		{KindManga, map[string]any{"status": "Publishing"}, ReleaseOngoing},
		{KindManga, map[string]any{"status": "On Hiatus"}, ReleaseHiatus},
		{KindManga, map[string]any{"status": "Discontinued"}, ReleaseCancelled},
		{KindManga, map[string]any{"status_in_country_of_origin": "311 Chapters + Prologue (Hiatus) 20 Volumes (Ongoing)"}, ReleaseHiatus},
		{KindManga, map[string]any{"status_in_country_of_origin": "600+ Chapters (Ongoing)"}, ReleaseOngoing},
		{KindMovie, map[string]any{"status": "Released", "release_date": "2021-10-22"}, ""},
		{KindMovie, map[string]any{"status": "Post Production"}, ReleaseUpcoming},
		{KindGame, map[string]any{"release_date": "2027-03-01"}, ReleaseUpcoming},
		{KindGame, map[string]any{"release_date": "2004-11-15"}, ""},
		{KindBook, map[string]any{"publish_date": "2027-01-05"}, ReleaseUpcoming},
		{KindTV, map[string]any{}, ""},
	} {
		if got := ReleaseStatus(tc.kind, tc.details, today); got != tc.want {
			t.Errorf("%s %v = %q, want %q", tc.kind, tc.details, got, tc.want)
		}
	}
}
