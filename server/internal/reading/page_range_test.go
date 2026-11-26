package reading_test

import (
	"testing"
	"time"

	"github.com/the-agency-labs/axiom/server/internal/reading"
	"github.com/the-agency-labs/axiom/server/internal/reading/readingtest"
)

// Opening on page 188, flipping back to 184 and reading forward again is the
// range 184-188, not "188 to 188".
func TestJournalPageRangeSurvivesFlippingBack(t *testing.T) {
	env, ctx := readingtest.NewEnv(t, t0), t.Context()
	at := t0.Add(-time.Hour)
	var stats []reading.PageStat
	for i, page := range []int64{188, 186, 184, 185, 188} {
		stats = append(stats, stat(page, at.Add(time.Duration(i)*time.Minute), 50))
	}
	if _, err := env.Reading.IngestKOReader(ctx, env.User, reading.KOBook{MD5: "flip", Title: "A Book", PageStats: stats}, ""); err != nil {
		t.Fatal(err)
	}
	days, err := env.Reading.Journal(ctx, env.User, at.AddDate(0, 0, -1), at.AddDate(0, 0, 1))
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range days {
		for _, b := range d.Books {
			if b.FromPage == nil || b.ToPage == nil || *b.FromPage != 184 || *b.ToPage != 188 {
				t.Fatalf("range %v-%v, want 184-188", b.FromPage, b.ToPage)
			}
			return
		}
	}
	t.Fatal("no reading logged")
}
