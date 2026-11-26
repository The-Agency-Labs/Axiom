package reading_test

import (
	"testing"
	"time"

	"github.com/the-agency-labs/axiom/server/internal/reading"
	"github.com/the-agency-labs/axiom/server/internal/reading/readingtest"
)

// The daily log merges every session of a day per book, newest day first,
// books in the order they were first read; merged duplicates count as one book.
func TestJournalMergesPerDayAndBook(t *testing.T) {
	env := readingtest.NewEnv(t, t0)
	day1 := time.Date(2026, 11, 23, 8, 0, 0, 0, time.UTC)
	day2 := time.Date(2026, 11, 24, 21, 0, 0, 0, time.UTC)
	ingest := func(md5, title string, stats ...reading.PageStat) reading.Document {
		t.Helper()
		res, err := env.Reading.IngestKOReader(ctx, env.User, reading.KOBook{MD5: md5, Title: title, Authors: "Brown, Pierce", TotalPages: ptr(int64(400)), PageStats: stats}, "")
		if err != nil {
			t.Fatal(err)
		}
		return res.Document
	}
	// Day 1: Golden Son morning + evening (two sessions), a short Iron Gold read.
	ingest("gs", "Golden Son (The Red Rising Trilogy, Book 2)",
		stat(10, day1, 600), stat(11, day1.Add(10*time.Minute), 600),
		stat(12, day1.Add(10*time.Hour), 600))
	ig := ingest("ig", "Brown, Pierce - Red Rising 04 - Iron Gold", stat(1, day1.Add(12*time.Hour), 120))
	// Day 2: a duplicate copy of Iron Gold, merged into the first.
	dup := ingest("ig2", "Iron Gold", stat(2, day2, 300))
	if _, err := env.Reading.Merge(ctx, dup, ig.ID); err != nil {
		t.Fatal(err)
	}
	ingest("ig", "Brown, Pierce - Red Rising 04 - Iron Gold", stat(3, day2.Add(30*time.Minute), 300))

	days, err := env.Reading.Journal(ctx, env.User, day1.AddDate(0, 0, -1), day2)
	if err != nil {
		t.Fatal(err)
	}
	if len(days) != 2 || days[0].Date != "2026-11-24" || days[1].Date != "2026-11-23" {
		t.Fatalf("days = %+v", days)
	}
	d2 := days[0]
	if len(d2.Books) != 1 || d2.Books[0].Title != "Red Rising 4: Iron Gold" || d2.Minutes != 10 || d2.Pages != 2 {
		t.Fatalf("day 2 (merged copies are one book) = %+v", d2)
	}
	d1 := days[1]
	if len(d1.Books) != 2 || d1.Minutes != 32 || d1.Pages != 4 {
		t.Fatalf("day 1 = %+v", d1)
	}
	gs := d1.Books[0]
	if gs.Title != "Red Rising 2: Golden Son" || gs.Minutes != 30 || gs.Pages != 3 || *gs.FromPage != 10 || *gs.ToPage != 12 || *gs.Series != "Red Rising" || *gs.SeriesNumber != 2 {
		t.Errorf("Golden Son = %+v", gs)
	}
	if d1.Books[1].Title != "Red Rising 4: Iron Gold" || d1.Books[1].Minutes != 2 {
		t.Errorf("Iron Gold = %+v", d1.Books[1])
	}
}

// Within a day, books are listed in reading order, not by time spent: a book
// finished in the morning comes before the sequel started that evening.
func TestJournalOrdersBooksByFirstRead(t *testing.T) {
	env := readingtest.NewEnv(t, t0)
	day := time.Date(2026, 11, 3, 9, 0, 0, 0, time.UTC)
	for _, b := range []struct {
		md5, title string
		at         time.Time
		secs       int64
	}{
		{"messiah", "Dune Messiah", day.Add(10 * time.Hour), 3600},
		{"dune", "Dune", day, 900},
	} {
		if _, err := env.Reading.IngestKOReader(ctx, env.User, reading.KOBook{MD5: b.md5, Title: b.title, Authors: "Frank Herbert", TotalPages: ptr(int64(500)),
			PageStats: []reading.PageStat{stat(1, b.at, b.secs)}}, ""); err != nil {
			t.Fatal(err)
		}
	}
	days, err := env.Reading.Journal(ctx, env.User, day, day)
	if err != nil {
		t.Fatal(err)
	}
	if len(days) != 1 || len(days[0].Books) != 2 || days[0].Books[0].Title != "Dune" || days[0].Books[1].Title != "Dune Messiah" {
		t.Fatalf("order: %+v", days)
	}
}
