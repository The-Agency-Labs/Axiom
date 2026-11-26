package reading_test

import (
	"testing"
	"time"

	"github.com/the-agency-labs/axiom/server/internal/reading"
	"github.com/the-agency-labs/axiom/server/internal/reading/readingtest"
)

// A linked book counts in the device's pages: progress shows the page on the
// screen, a new layout re-expresses it, and a metadata refresh keeps it.
func TestLinkedBookFollowsDevicePages(t *testing.T) {
	env := readingtest.NewEnv(t, t0)
	item, doc := linkedBook(t, env, 304)
	read := func(page, pages int64, at time.Time) {
		t.Helper()
		if _, err := env.Reading.IngestKOReader(ctx, env.User, reading.KOBook{MD5: "h1", TotalPages: ptr(pages),
			PageStats: []reading.PageStat{{Page: page, StartTime: at.Unix(), Duration: 60, TotalPages: ptr(pages)}}}, "Kindle"); err != nil {
			t.Fatal(err)
		}
	}
	check := func(progress, total int64) {
		t.Helper()
		entry, err := env.Library.CurrentEntry(ctx, env.User, item.ID)
		if err != nil {
			t.Fatal(err)
		}
		got, err := env.Catalog.GetItem(ctx, item.ID)
		if err != nil {
			t.Fatal(err)
		}
		if entry.Progress != progress || got.Total.Int64 != total {
			t.Fatalf("progress %d of %d, want %d of %d", entry.Progress, got.Total.Int64, progress, total)
		}
	}
	read(189, 260, t0.Add(-2*time.Hour))
	if _, err := env.Reading.LinkDocument(ctx, doc, item); err != nil {
		t.Fatal(err)
	}
	// The document was created with 300 pages, then the upload said 260.
	check(189, 260)

	// A bigger font: the same place, in the new pages.
	if err := env.Reading.SaveTOC(ctx, env.User, "h1", reading.DocFields{TotalPages: ptr(int64(300))}, 300, []reading.TOCEntry{{Title: "Chapter 1", Page: 1}}); err != nil {
		t.Fatal(err)
	}
	check(218, 300)
	read(225, 300, t0.Add(-time.Hour))
	check(225, 300)

	if _, err := env.Catalog.RefreshItem(ctx, item); err != nil {
		t.Fatal(err)
	}
	check(225, 300)
}
