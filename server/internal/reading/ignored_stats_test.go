package reading_test

import (
	"testing"
	"time"

	"github.com/the-agency-labs/axiom/server/internal/reading"
	"github.com/the-agency-labs/axiom/server/internal/reading/readingtest"
)

// A file the reader marked ignored (a quickstart guide) isn't reading.
func TestStatsLeaveOutIgnoredDocuments(t *testing.T) {
	env := readingtest.NewEnv(t, t0)
	at := t0.Add(-time.Hour)
	res, err := env.Reading.IngestKOReader(ctx, env.User, reading.KOBook{MD5: "guide", Title: "Guide",
		PageStats: []reading.PageStat{stat(1, at, 60), stat(2, at.Add(time.Minute), 60)}}, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := env.Reading.Ignore(ctx, res.Document, true); err != nil {
		t.Fatal(err)
	}
	days, err := env.Reading.DailyStats(ctx, env.User, env.Reading.Today().AddDate(0, 0, -1), env.Reading.Today())
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range days {
		if d.Pages != 0 || d.Sessions != 0 {
			t.Fatalf("ignored document counted: %+v", d)
		}
	}
	if current, longest, err := env.Reading.Streaks(ctx, env.User); err != nil || current != 0 || longest != 0 {
		t.Fatalf("streak %d/%d %v", current, longest, err)
	}
}
