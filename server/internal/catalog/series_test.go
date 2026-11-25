package catalog_test

import (
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/the-agency-labs/encompass/server/internal/catalog"
	"github.com/the-agency-labs/encompass/server/internal/catalog/catalogtest"
	"github.com/the-agency-labs/encompass/server/internal/db"
	"github.com/the-agency-labs/encompass/server/internal/providers"
)

func TestParseSeriesString(t *testing.T) {
	for _, tc := range []struct {
		in   string
		name string
		pos  float64
	}{
		{"Red Rising Saga #2", "Red Rising", 2},
		{"The Red Rising Trilogy, Book 2", "Red Rising", 2},
		{"Red Rising", "Red Rising", 0},
		{"Mistborn ; v. 1", "Mistborn", 1},
		{"The Wheel of Time (Vol. 3)", "The Wheel of Time", 3},
		{"Dune, 3", "Dune", 3},
		{"Fahrenheit 451", "Fahrenheit 451", 0},
		{"Discworld #0.5", "Discworld", 0.5},
		{"Notebook 2", "Notebook 2", 0},
		{"The Expanse, Book 4", "The Expanse", 4},
	} {
		name, pos := catalog.ParseSeriesString(tc.in)
		if name != tc.name || pos != tc.pos {
			t.Errorf("ParseSeriesString(%q) = %q, %v; want %q, %v", tc.in, name, pos, tc.name, tc.pos)
		}
	}
}

func TestSeriesKeyUnifiesNames(t *testing.T) {
	want := catalog.SeriesKey("Red Rising")
	for _, n := range []string{"The Red Rising Trilogy", "Red Rising Saga", "red rising", "Red-Rising Series", "The Red Rising"} {
		if got := catalog.SeriesKey(n); got != want {
			t.Errorf("SeriesKey(%q) = %q, want %q", n, got, want)
		}
	}
	if got := catalog.NormalizeSeriesName("The Red Rising Trilogy"); got != "Red Rising" {
		t.Errorf("NormalizeSeriesName = %q", got)
	}
	if catalog.SeriesKey("The Wheel of Time") == catalog.SeriesKey("Wheel") {
		t.Error("distinct series share a key")
	}
}

type seriesEnv struct {
	*catalogtest.Env
	hc *catalogtest.Hardcover
	ol *catalogtest.OpenLibrary
}

func newSeriesEnv(t *testing.T) seriesEnv {
	t.Helper()
	env := catalogtest.NewEnv(t, t0)
	e := seriesEnv{Env: env, hc: catalogtest.NewHardcover(), ol: catalogtest.NewOpenLibrary()}
	env.Catalog.Reg.P.Hardcover = e.hc
	env.Catalog.Reg.P.OpenLibrary = e.ol
	return e
}

func (e seriesEnv) seriesBooks(t *testing.T, seriesID int64) []db.SeriesBook {
	t.Helper()
	rows, err := e.Conn.Query(`SELECT series_id, position, item_id, source, external_id, title, image, year, is_primary
		FROM series_books WHERE series_id = ? ORDER BY position, external_id`, seriesID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []db.SeriesBook
	for rows.Next() {
		var b db.SeriesBook
		if err := rows.Scan(&b.SeriesID, &b.Position, &b.ItemID, &b.Source, &b.ExternalID, &b.Title, &b.Image, &b.Year, &b.IsPrimary); err != nil {
			t.Fatal(err)
		}
		out = append(out, b)
	}
	return out
}

func (e seriesEnv) seriesByKey(t *testing.T, source, id string) db.Series {
	t.Helper()
	s, err := e.Q.GetSeriesByKey(ctx, db.GetSeriesByKeyParams{Source: source, ExternalID: id})
	if err != nil {
		t.Fatalf("series %s/%s: %v", source, id, err)
	}
	return s
}

func redRising() []providers.SeriesBook {
	return []providers.SeriesBook{
		catalogtest.SeriesBook(1, "101", "Red Rising"),
		catalogtest.SeriesBook(2, "102", "Golden Son"),
		catalogtest.SeriesBook(3, "103", "Morning Star"),
	}
}

func TestEnsureItemStoresHardcoverSeriesWithGaps(t *testing.T) {
	e := newSeriesEnv(t)
	e.hc.AddBook("102", "Golden Son").AddSeries("55", "The Red Rising Trilogy", redRising()...)

	item, err := e.Catalog.EnsureItem(ctx, catalog.KindBook, catalog.SourceHardcover, "102")
	if err != nil {
		t.Fatal(err)
	}
	s := e.seriesByKey(t, "hardcover", "55")
	eq(t, "name normalised", s.Name, "Red Rising")
	eq(t, "books_count", s.BooksCount, int64(3))
	books := e.seriesBooks(t, s.ID)
	eq(t, "rows (gaps included)", len(books), 3)
	for _, b := range books {
		linked := b.ItemID.Valid && b.ItemID.Int64 == item.ID
		eq(t, "linked "+b.ExternalID, linked, b.ExternalID == "102")
	}
	// The returned item carries its series for JSON.
	got := catalog.ItemSeriesFromMeta(item.Meta)
	if got == nil || got.ID != s.ID || got.Name != "Red Rising" || got.Position != 2 {
		t.Fatalf("item series = %+v", got)
	}

	// A later book of the series links to the row that was waiting for it.
	e.hc.AddBook("103", "Morning Star")
	third, err := e.Catalog.EnsureItem(ctx, catalog.KindBook, catalog.SourceHardcover, "103")
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range e.seriesBooks(t, s.ID) {
		if b.ExternalID == "103" && (!b.ItemID.Valid || b.ItemID.Int64 != third.ID) {
			t.Errorf("row 103 not linked: %+v", b)
		}
	}
	if got := catalog.ItemSeriesFromMeta(third.Meta); got == nil || got.Position != 3 {
		t.Fatalf("third item series = %+v", got)
	}
}

func TestMultipleSeriesFirstIsPrimary(t *testing.T) {
	e := newSeriesEnv(t)
	e.hc.AddBook("7", "Crossover")
	e.hc.AddSeries("1", "Main Saga", catalogtest.SeriesBook(4, "7", "Crossover"))
	e.hc.AddSeries("2", "Side Stories", catalogtest.SeriesBook(1, "7", "Crossover"))
	item, err := e.Catalog.EnsureItem(ctx, catalog.KindBook, catalog.SourceHardcover, "7")
	if err != nil {
		t.Fatal(err)
	}
	main, side := e.seriesByKey(t, "hardcover", "1"), e.seriesByKey(t, "hardcover", "2")
	eq(t, "main primary", e.seriesBooks(t, main.ID)[0].IsPrimary, int64(1))
	eq(t, "side primary", e.seriesBooks(t, side.ID)[0].IsPrimary, int64(0))
	if got := catalog.ItemSeriesFromMeta(item.Meta); got == nil || got.ID != main.ID || got.Position != 4 {
		t.Fatalf("primary = %+v", got)
	}
}

func TestOpenLibraryFallbackNormalisesAndMerges(t *testing.T) {
	e := newSeriesEnv(t)
	e.ol.AddBook("OL1M", "Golden Son", "Red Rising Saga #2")
	e.ol.AddBook("OL2M", "Morning Star", "The Red Rising Trilogy, Book 3")
	a, err := e.Catalog.EnsureItem(ctx, catalog.KindBook, catalog.SourceOpenLibrary, "OL1M")
	if err != nil {
		t.Fatal(err)
	}
	b, err := e.Catalog.EnsureItem(ctx, catalog.KindBook, catalog.SourceOpenLibrary, "OL2M")
	if err != nil {
		t.Fatal(err)
	}
	sa, sb := catalog.ItemSeriesFromMeta(a.Meta), catalog.ItemSeriesFromMeta(b.Meta)
	if sa == nil || sb == nil || sa.ID != sb.ID {
		t.Fatalf("books not in one series: %+v %+v", sa, sb)
	}
	eq(t, "name", sa.Name, "Red Rising")
	eq(t, "pos a", sa.Position, 2.0)
	eq(t, "pos b", sb.Position, 3.0)
	s, _ := e.Q.GetSeries(ctx, sa.ID)
	eq(t, "source", s.Source, "openlibrary")
	eq(t, "rows", len(e.seriesBooks(t, sa.ID)), 2)
}

func TestOpenLibraryJoinsHardcoverSeriesAndClaimsRow(t *testing.T) {
	e := newSeriesEnv(t)
	e.hc.AddBook("101", "Red Rising").AddSeries("55", "Red Rising", redRising()...)
	if _, err := e.Catalog.EnsureItem(ctx, catalog.KindBook, catalog.SourceHardcover, "101"); err != nil {
		t.Fatal(err)
	}
	e.ol.AddBook("OL9M", "Golden Son (OL edition)", "The Red Rising Trilogy, Book 2")
	ol, err := e.Catalog.EnsureItem(ctx, catalog.KindBook, catalog.SourceOpenLibrary, "OL9M")
	if err != nil {
		t.Fatal(err)
	}
	s := e.seriesByKey(t, "hardcover", "55")
	if got := catalog.ItemSeriesFromMeta(ol.Meta); got == nil || got.ID != s.ID {
		t.Fatalf("OL book not in the Hardcover series: %+v", got)
	}
	books := e.seriesBooks(t, s.ID)
	eq(t, "no duplicate row at position 2", len(books), 3)
	if !books[1].ItemID.Valid || books[1].ItemID.Int64 != ol.ID {
		t.Errorf("position 2 not claimed by the OL item: %+v", books[1])
	}
}

func TestSeriesFailureNeverFailsEnsureItemAndBackfillRetries(t *testing.T) {
	e := newSeriesEnv(t)
	e.hc.AddBook("102", "Golden Son").AddSeries("55", "Red Rising", redRising()...)
	e.hc.SeriesErr = errors.New("hardcover down")
	item, err := e.Catalog.EnsureItem(ctx, catalog.KindBook, catalog.SourceHardcover, "102")
	if err != nil {
		t.Fatalf("EnsureItem failed on series error: %v", err)
	}
	if catalog.ItemSeriesFromMeta(item.Meta) != nil {
		t.Fatal("series stored despite error")
	}

	// Retried the next day, not the same day.
	e.hc.SeriesErr = nil
	if n, err := e.Catalog.BackfillSeries(ctx, 10); err != nil || n != 0 {
		t.Fatalf("same-day backfill = %d, %v", n, err)
	}
	e.Advance(26 * time.Hour)
	if n, err := e.Catalog.BackfillSeries(ctx, 10); err != nil || n != 1 {
		t.Fatalf("next-day backfill = %d, %v", n, err)
	}
	got, _ := e.Catalog.GetItem(ctx, item.ID)
	if s := catalog.ItemSeriesFromMeta(got.Meta); s == nil || s.Name != "Red Rising" {
		t.Fatalf("after backfill: %+v", s)
	}
}

// bookOnly hides the series methods of the fake, like a client that predates them.
type bookOnly struct{ catalog.Hardcover }

func TestBackfillCoversBooksNeverChecked(t *testing.T) {
	e := newSeriesEnv(t)
	e.hc.AddBook("101", "Red Rising").AddBook("999", "Standalone").AddSeries("55", "Red Rising", redRising()...)
	e.Catalog.Reg.P.Hardcover = bookOnly{e.hc}
	a, _ := e.Catalog.EnsureItem(ctx, catalog.KindBook, catalog.SourceHardcover, "101")
	b, _ := e.Catalog.EnsureItem(ctx, catalog.KindBook, catalog.SourceHardcover, "999")
	e.Catalog.Reg.P.Hardcover = e.hc

	if n, err := e.Catalog.BackfillSeries(ctx, 1); err != nil || n != 1 {
		t.Fatalf("limited batch = %d, %v", n, err)
	}
	if n, err := e.Catalog.BackfillSeries(ctx, 10); err != nil || n != 1 {
		t.Fatalf("second batch = %d, %v", n, err)
	}
	if n, _ := e.Catalog.BackfillSeries(ctx, 10); n != 0 {
		t.Fatalf("third batch = %d, want 0 (everything checked)", n)
	}
	ga, _ := e.Catalog.GetItem(ctx, a.ID)
	gb, _ := e.Catalog.GetItem(ctx, b.ID)
	if catalog.ItemSeriesFromMeta(ga.Meta) == nil {
		t.Error("Red Rising has no series after backfill")
	}
	if catalog.ItemSeriesFromMeta(gb.Meta) != nil {
		t.Error("standalone got a series")
	}
	// A book in no series is asked again after 30 days.
	e.Advance(31 * 24 * time.Hour)
	if n, _ := e.Catalog.BackfillSeries(ctx, 10); n != 1 {
		t.Fatalf("after 30 days = %d, want 1 (the standalone)", n)
	}
}

func TestBackfillRefreshesStaleSeries(t *testing.T) {
	e := newSeriesEnv(t)
	e.hc.AddBook("101", "Red Rising").AddSeries("55", "Red Rising", redRising()[:2]...)
	if _, err := e.Catalog.EnsureItem(ctx, catalog.KindBook, catalog.SourceHardcover, "101"); err != nil {
		t.Fatal(err)
	}
	s := e.seriesByKey(t, "hardcover", "55")
	eq(t, "rows before", len(e.seriesBooks(t, s.ID)), 2)

	// The provider adds a book and drops another; nothing changes until 30 days pass.
	e.hc.Calls = map[string]int{}
	e.hc.SetSeries("55", "Red Rising", catalogtest.SeriesBook(1, "101", "Red Rising"), catalogtest.SeriesBook(3, "103", "Morning Star"))
	e.Advance(10 * 24 * time.Hour)
	if _, err := e.Catalog.BackfillSeries(ctx, 10); err != nil {
		t.Fatal(err)
	}
	eq(t, "no refresh yet", e.hc.Calls["Series"], 0)
	e.Advance(21 * 24 * time.Hour)
	if _, err := e.Catalog.BackfillSeries(ctx, 10); err != nil {
		t.Fatal(err)
	}
	eq(t, "refreshed", e.hc.Calls["Series"], 1)
	books := e.seriesBooks(t, s.ID)
	if len(books) != 2 || books[1].ExternalID != "103" {
		t.Fatalf("rows after refresh: %+v", books)
	}
}

func TestFetchSeriesOnDemand(t *testing.T) {
	e := newSeriesEnv(t)
	e.hc.AddBook("102", "Golden Son").AddSeries("55", "The Red Rising Trilogy", redRising()...)
	s, err := e.Catalog.FetchSeries(ctx, catalog.SourceHardcover, "55")
	if err != nil {
		t.Fatal(err)
	}
	eq(t, "name", s.Name, "Red Rising")
	eq(t, "rows", len(e.seriesBooks(t, s.ID)), 3)
	// Fresh: no second provider call.
	if _, err := e.Catalog.FetchSeries(ctx, catalog.SourceHardcover, "55"); err != nil {
		t.Fatal(err)
	}
	eq(t, "calls", e.hc.Calls["Series"], 1)
	// A book added afterwards finds its row waiting.
	item, err := e.Catalog.EnsureItem(ctx, catalog.KindBook, catalog.SourceHardcover, "102")
	if err != nil {
		t.Fatal(err)
	}
	var linked sql.NullInt64
	for _, b := range e.seriesBooks(t, s.ID) {
		if b.ExternalID == "102" {
			linked = b.ItemID
		}
	}
	if !linked.Valid || linked.Int64 != item.ID {
		t.Fatalf("row 102 link = %v", linked)
	}
	if _, err := e.Catalog.FetchSeries(ctx, catalog.SourceHardcover, "404"); !errors.Is(err, catalog.ErrNotFound) {
		t.Fatalf("unknown series: %v", err)
	}
	if _, err := e.Catalog.FetchSeries(ctx, "goodreads", "1"); !errors.Is(err, catalog.ErrUnsupported) {
		t.Fatalf("unsupported source: %v", err)
	}
}

func TestRefreshItemKeepsSeriesInMeta(t *testing.T) {
	e := newSeriesEnv(t)
	e.hc.AddBook("102", "Golden Son").AddSeries("55", "Red Rising", redRising()...)
	item, err := e.Catalog.EnsureItem(ctx, catalog.KindBook, catalog.SourceHardcover, "102")
	if err != nil {
		t.Fatal(err)
	}
	// A refresh rewrites meta from the provider; the series must survive it,
	// even when the series call fails.
	e.hc.SeriesErr = errors.New("down")
	got, err := e.Catalog.RefreshItem(ctx, item)
	if err != nil {
		t.Fatal(err)
	}
	if s := catalog.ItemSeriesFromMeta(got.Meta); s == nil || s.Position != 2 {
		t.Fatalf("series lost on refresh: %+v", s)
	}
}
