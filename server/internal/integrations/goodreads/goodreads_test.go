package goodreads

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/the-agency-labs/encompass/server/internal/catalog/catalogtest"
	"github.com/the-agency-labs/encompass/server/internal/db"
	"github.com/the-agency-labs/encompass/server/internal/library"
	"github.com/the-agency-labs/encompass/server/internal/providers"
)

var (
	ctx = context.Background()
	t0  = time.Date(2024, 6, 1, 12, 0, 0, 0, time.UTC)
)

// fakeBooks is a catalog book provider (Hardcover or Open Library) answering
// searches from a table and recording what was asked.
type fakeBooks struct {
	source string
	hits   map[string]string // query -> media id
	asked  []string
}

func (f *fakeBooks) Search(_ context.Context, query string, _ int) (providers.SearchPage, error) {
	f.asked = append(f.asked, query)
	id, ok := f.hits[query]
	if !ok {
		return providers.SearchPage{"results": []any{}}, nil
	}
	return providers.SearchPage{"results": []any{map[string]any{"media_id": id, "source": f.source, "media_type": "book", "title": "Hit " + id, "image": "https://img/" + id}}}, nil
}

func (f *fakeBooks) Book(_ context.Context, id string) (providers.Metadata, error) {
	return providers.Metadata{"media_id": id, "source": f.source, "media_type": "book", "title": "Book " + id, "image": "https://img/" + id, "max_progress": 300, "details": map[string]any{}}, nil
}

type fixture struct {
	imp  *Importer
	user int64
	hard *fakeBooks
	open *fakeBooks
	lib  *library.Service
	q    *db.Queries
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	env := catalogtest.NewEnv(t, t0)
	hard := &fakeBooks{source: "hardcover", hits: map[string]string{}}
	open := &fakeBooks{source: "openlibrary", hits: map[string]string{}}
	env.Catalog.Reg.P.Hardcover = hard
	env.Catalog.Reg.P.OpenLibrary = open
	lib := library.New(env.Conn, env.Q, env.Catalog)
	lib.Now = env.Now
	imp := New(env.Catalog, lib, env.Q)
	imp.Now = env.Now
	return &fixture{imp: imp, user: env.User(t, "owner"), hard: hard, open: open, lib: lib, q: env.Q}
}

const header = `Book Id,Title,Author,ISBN,ISBN13,My Rating,Number of Pages,Date Read,Date Added,Bookshelves,Exclusive Shelf,Private Notes` + "\n"

func (f *fixture) entry(t *testing.T, source, id string) db.Entry {
	t.Helper()
	item, err := f.q.GetItemByKey(ctx, db.GetItemByKeyParams{Source: source, ExternalID: id, Kind: "book"})
	if err != nil {
		t.Fatalf("item %s %s: %v", source, id, err)
	}
	e, err := f.lib.CurrentEntry(ctx, f.user, item.ID)
	if err != nil {
		t.Fatalf("entry %s %s: %v", source, id, err)
	}
	return e
}

func (f *fixture) hasItem(source, id string) bool {
	_, err := f.q.GetItemByKey(ctx, db.GetItemByKeyParams{Source: source, ExternalID: id, Kind: "book"})
	return err == nil
}

func TestShelvesMapToStatuses(t *testing.T) {
	f := newFixture(t)
	f.hard.hits["9780000000001"] = "1"
	f.hard.hits["9780000000002"] = "2"
	f.hard.hits["9780000000003"] = "3"
	f.hard.hits["9780000000004"] = "4"
	csv := header +
		`1,Read Book,Ann Author,="0000000001",="9780000000001",4,320,2023/05/14,2023/01/02,favorites,read,"Loved it, truly"` + "\n" +
		`2,Now Book,Bo Writer,"","=""9780000000002""",0,200,,2024/05/01,,currently-reading,` + "\n" +
		`3,Later Book,Cy Scribe,,9780000000003,,100,,2024/04/01,wishlist,to-read,` + "\n" +
		`4,Gave Up,Di Pen,,9780000000004,2,100,,2024/04/01,,did-not-finish,` + "\n" +
		`5,Odd Shelf,Ed Ink,,9780000000005,,100,,2024/04/01,,my-custom-shelf,` + "\n"
	res, err := f.imp.Import(ctx, f.user, []byte(csv))
	if err != nil {
		t.Fatal(err)
	}
	read := f.entry(t, "hardcover", "1")
	if read.Status != library.Completed || read.Score.Float64 != 8 || read.Notes != "Loved it, truly" {
		t.Fatalf("read = %+v", read)
	}
	if read.Progress != 320 {
		t.Fatalf("a read book gets its page count: %d", read.Progress)
	}
	if !strings.HasPrefix(read.FinishedAt.String, "2023-05-14") {
		t.Fatalf("finished = %v", read.FinishedAt)
	}
	// The newest of Date Read / Date Added dates the change.
	evs, _ := f.q.ListEntryEvents(ctx, read.ID)
	if len(evs) == 0 || !strings.HasPrefix(evs[0].At, "2023-05-14") {
		t.Fatalf("events = %+v", evs)
	}
	now := f.entry(t, "hardcover", "2")
	if now.Status != library.InProgress || now.Progress != 0 || now.Score.Valid {
		t.Fatalf("now = %+v", now)
	}
	if e := f.entry(t, "hardcover", "3"); e.Status != library.Planning {
		t.Fatalf("later = %+v", e)
	}
	if e := f.entry(t, "hardcover", "4"); e.Status != library.Dropped || e.Score.Float64 != 4 {
		t.Fatalf("dnf = %+v", e)
	}
	// A custom shelf is skipped before any provider call.
	for _, q := range append(f.hard.asked, f.open.asked...) {
		if strings.Contains(q, "9780000000005") {
			t.Fatal("custom shelf row hit a provider")
		}
	}
	if res.Counts["books"] != 4 || res.Counts["skipped"] != 1 || len(res.Warnings) != 1 || !strings.Contains(res.Warnings[0], "Unsupported shelf") {
		t.Fatalf("res = %+v", res)
	}
}

func TestMatchingFallsBackIsbnThenTitleThenOpenLibrary(t *testing.T) {
	f := newFixture(t)
	// ISBN13 misses, ISBN hits.
	f.hard.hits["1111111111"] = "10"
	// Only the title+author query hits.
	f.hard.hits["Lost Title Some Author"] = "11"
	// Hardcover knows nothing: Open Library finds it by ISBN13.
	f.open.hits["isbn:9782222222222"] = "OL5M"
	csv := header +
		`1,A,X,="1111111111",="9781111111111",,100,,,,to-read,` + "\n" +
		`2,Lost Title,Some Author,,,,100,,,,to-read,` + "\n" +
		`3,Open Only,Q,,9782222222222,,100,,,,to-read,` + "\n" +
		`4,Nowhere,Q,,9783333333333,,100,,,,to-read,` + "\n"
	res, err := f.imp.Import(ctx, f.user, []byte(csv))
	if err != nil {
		t.Fatal(err)
	}
	f.entry(t, "hardcover", "10")
	f.entry(t, "hardcover", "11")
	f.entry(t, "openlibrary", "OL5M")
	if f.hasItem("hardcover", "9783333333333") {
		t.Fatal("unmatched book created")
	}
	if res.Counts["unmatched"] != 1 || len(res.Warnings) != 1 || !strings.Contains(res.Warnings[0], "Nowhere") {
		t.Fatalf("res = %+v", res)
	}
	// Order: ISBN13 before ISBN before title, Hardcover before Open Library.
	want := []string{"9781111111111", "1111111111"}
	if len(f.hard.asked) < 2 || f.hard.asked[0] != want[0] || f.hard.asked[1] != want[1] {
		t.Fatalf("hardcover asked %v", f.hard.asked)
	}
}

func TestAdditiveOnExistingEntries(t *testing.T) {
	f := newFixture(t)
	f.hard.hits["9780000000009"] = "9"
	// The owner already tracks the book further along than the export says.
	item, err := f.imp.Catalog.EnsureItem(ctx, "book", "hardcover", "9")
	if err != nil {
		t.Fatal(err)
	}
	if err := f.lib.SetProgress(ctx, f.user, item.ID, 250, library.SourceManual, t0); err != nil {
		t.Fatal(err)
	}
	if err := f.lib.SetScore(ctx, f.user, item.ID, ptr(9.5), library.SourceManual); err != nil {
		t.Fatal(err)
	}
	csv := header + `1,Tracked,A,,9780000000009,2,200,,2024/01/01,,currently-reading,note` + "\n"
	res, err := f.imp.Import(ctx, f.user, []byte(csv))
	if err != nil {
		t.Fatal(err)
	}
	e := f.entry(t, "hardcover", "9")
	if e.Progress != 250 || e.Score.Float64 != 9.5 || e.Status != library.InProgress {
		t.Fatalf("lowered or overwritten: %+v", e)
	}
	if e.Notes != "note" { // missing notes are filled
		t.Fatalf("notes = %q", e.Notes)
	}
	if res.Counts["updated"] != 1 {
		t.Fatalf("counts = %v", res.Counts)
	}
	// The same file again, now saying "read": it completes with the page count.
	csv = header + `1,Tracked,A,,9780000000009,2,300,2024/02/02,2024/01/01,,read,` + "\n"
	if _, err := f.imp.Import(ctx, f.user, []byte(csv)); err != nil {
		t.Fatal(err)
	}
	e = f.entry(t, "hardcover", "9")
	if e.Status != library.Completed || e.Progress != 300 || !strings.HasPrefix(e.FinishedAt.String, "2024-02-02") {
		t.Fatalf("completed = %+v", e)
	}
	// And back to to-read: nothing moves back.
	csv = header + `1,Tracked,A,,9780000000009,,100,,2024/01/01,,to-read,` + "\n"
	if _, err := f.imp.Import(ctx, f.user, []byte(csv)); err != nil {
		t.Fatal(err)
	}
	if e = f.entry(t, "hardcover", "9"); e.Status != library.Completed || e.Progress != 300 {
		t.Fatalf("moved back: %+v", e)
	}
}

func ptr[T any](v T) *T { return &v }

func TestRatings(t *testing.T) {
	for raw, want := range map[string]float64{"5": 10, "5.0": 10, "1": 2, "3.5": 7, "4.5": 9} {
		if got := parseRating(raw); got == nil || *got != want {
			t.Errorf("rating %q = %v, want %v", raw, got, want)
		}
	}
	for _, raw := range []string{"", "0", "6", "x", "-1"} {
		if got := parseRating(raw); got != nil {
			t.Errorf("rating %q = %v, want none", raw, *got)
		}
	}
}

func TestCleanISBN(t *testing.T) {
	for in, want := range map[string]string{`="9780000000001"`: "9780000000001", "": "", `="080442957x"`: "080442957X", "978-0-00": "978000"} {
		if got := cleanISBN(in); got != want {
			t.Errorf("cleanISBN(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestBadFile(t *testing.T) {
	f := newFixture(t)
	if _, err := f.imp.Import(ctx, f.user, []byte{0xff, 0xfe, 0x00, 0x80}); err == nil {
		t.Fatal("expected an error for a non-text file")
	}
	if _, err := f.imp.Stash(ctx, f.user, []byte{0xff, 0xfe, 0x80}); err == nil {
		t.Fatal("stash should refuse a non-text file")
	}
	// A BOM and a header-only file are fine.
	if res, err := f.imp.Import(ctx, f.user, []byte("\xef\xbb\xbf"+header)); err != nil || len(res.Warnings) != 0 {
		t.Fatalf("res = %+v err = %v", res, err)
	}
}

func TestUploadJobWritesStatusAndClearsFile(t *testing.T) {
	f := newFixture(t)
	f.hard.hits["9780000000001"] = "1"
	key, err := f.imp.Stash(ctx, f.user, []byte(header+`1,T,A,,9780000000001,,10,,,,to-read,`+"\n"+`2,Nope,A,,9789999999999,,10,,,,to-read,`+"\n"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.imp.RunUpload(ctx, f.user, key); err != nil {
		t.Fatal(err)
	}
	raw, err := f.q.GetKV(ctx, "source:"+itoa(f.user)+":goodreads")
	if err != nil {
		t.Fatal(err)
	}
	var st struct {
		Name     string         `json:"name"`
		OK       bool           `json:"ok"`
		Counts   map[string]int `json:"counts"`
		Failures []string       `json:"failures"`
		Message  string         `json:"message"`
	}
	if err := json.Unmarshal([]byte(raw), &st); err != nil {
		t.Fatal(err)
	}
	if st.Name != "goodreads" || st.OK || st.Counts["books"] != 1 || len(st.Failures) != 1 || st.Message == "" {
		t.Fatalf("status = %+v", st)
	}
	if v, _ := f.q.GetKV(ctx, key); v != "" {
		t.Fatal("the upload should be cleared")
	}
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }
