package reading_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/the-agency-labs/axiom/server/internal/catalog"
	"github.com/the-agency-labs/axiom/server/internal/library"
	"github.com/the-agency-labs/axiom/server/internal/reading"
	"github.com/the-agency-labs/axiom/server/internal/reading/readingtest"
)

var (
	ctx = context.Background()
	t0  = time.Date(2026, 11, 25, 12, 0, 0, 0, time.UTC)
)

func ptr[T any](v T) *T { return &v }

func stat(page int64, at time.Time, secs int64) reading.PageStat {
	return reading.PageStat{Page: page, StartTime: at.Unix(), Duration: secs}
}

func TestIngestIsIdempotentAndGroupsSessions(t *testing.T) {
	env := readingtest.NewEnv(t, t0)
	day := time.Date(2026, 11, 24, 20, 0, 0, 0, time.UTC)
	book := reading.KOBook{MD5: "ABCDEF", Title: "A Book", Authors: "A. Writer", TotalPages: ptr(int64(300)), PageStats: []reading.PageStat{
		stat(1, day, 60),
		stat(2, day.Add(1*time.Minute), 90),
		stat(2, day.Add(3*time.Minute), 30),               // re-read of page 2: same session, one distinct page
		stat(3, day.Add(3*time.Minute+40*time.Second), 3), // under 5 s: dropped
		stat(10, day.Add(2*time.Hour), 120),               // after a long pause: new session
	}}
	res, err := env.Reading.IngestKOReader(ctx, env.User, book, "Kindle")
	if err != nil {
		t.Fatal(err)
	}
	if res.Added != 4 || res.Document.DocumentHash != "abcdef" {
		t.Fatalf("added=%d hash=%s", res.Added, res.Document.DocumentHash)
	}
	again, err := env.Reading.IngestKOReader(ctx, env.User, book, "Kindle")
	if err != nil || again.Added != 0 {
		t.Fatalf("resend added=%d err=%v", again.Added, err)
	}

	rows, err := env.Reading.ListSessions(ctx, env.User, day.AddDate(0, 0, -1), day.AddDate(0, 0, 1), 100)
	if err != nil || len(rows) != 2 {
		t.Fatalf("sessions = %d, %v", len(rows), err)
	}
	newest, oldest := rows[0].Session, rows[1].Session
	if oldest.PagesRead != 2 || oldest.DurationSeconds != 180 || oldest.StartPage.Int64 != 1 || oldest.EndPage.Int64 != 2 || oldest.Device != "Kindle" {
		t.Errorf("first session = %+v", oldest)
	}
	if newest.PagesRead != 1 || newest.DurationSeconds != 120 {
		t.Errorf("second session = %+v", newest)
	}

	docs, err := env.Reading.Summary(ctx, env.User)
	if err != nil || len(docs) != 1 || docs[0].Pages != 3 || docs[0].Minutes != 5 || docs[0].Sessions != 2 {
		t.Fatalf("summary = %+v %v", docs, err)
	}
}

func TestStatsAndStreaks(t *testing.T) {
	env := readingtest.NewEnv(t, t0) // today is 2026-11-25
	read := func(daysAgo int, page int64) {
		at := t0.AddDate(0, 0, -daysAgo).Add(-3 * time.Hour)
		if _, err := env.Reading.IngestKOReader(ctx, env.User, reading.KOBook{MD5: "d1", PageStats: []reading.PageStat{stat(page, at, 300)}}, ""); err != nil {
			t.Fatal(err)
		}
	}
	// Read today, yesterday, 2 days ago, then a gap, then a 4-day run ending 10 days ago.
	for i, d := range []int{0, 1, 2, 10, 11, 12, 13} {
		read(d, int64(i+1))
	}
	cur, longest, err := env.Reading.Streaks(ctx, env.User)
	if err != nil || cur != 3 || longest != 4 {
		t.Fatalf("streaks = %d, %d, %v", cur, longest, err)
	}
	start, end, err := env.Reading.DateRange(nil, nil)
	if err != nil || end.Format("2006-01-02") != "2026-11-25" || start.Format("2006-01-02") != "2026-10-27" {
		t.Fatalf("default range = %v %v %v", start, end, err)
	}
	days, err := env.Reading.DailyStats(ctx, env.User, start, end)
	if err != nil || len(days) != 30 {
		t.Fatalf("days = %d %v", len(days), err)
	}
	last := days[len(days)-1]
	if last.Date != "2026-11-25" || last.Pages != 1 || last.Minutes != 5 || last.Sessions != 1 {
		t.Errorf("today = %+v", last)
	}
	if days[len(days)-5].Pages != 0 {
		t.Errorf("a gap day has pages: %+v", days[len(days)-5])
	}

	// Yesterday only: the streak isn't broken until the day is over.
	env2 := readingtest.NewEnv(t, t0)
	if _, err := env2.Reading.IngestKOReader(ctx, env2.User, reading.KOBook{MD5: "d2", PageStats: []reading.PageStat{stat(1, t0.AddDate(0, 0, -1), 60)}}, ""); err != nil {
		t.Fatal(err)
	}
	if cur, _, _ := env2.Reading.Streaks(ctx, env2.User); cur != 1 {
		t.Errorf("streak ending yesterday = %d", cur)
	}

	later := time.Date(2026, 11, 25, 0, 0, 0, 0, time.UTC)
	if _, _, err := env.Reading.DateRange(&end, &later); err != nil {
		t.Errorf("equal start/end rejected: %v", err)
	}
	early := later.AddDate(0, 0, -4000)
	if _, _, err := env.Reading.DateRange(&early, &later); !errors.Is(err, reading.ErrInvalid) {
		t.Errorf("huge range accepted: %v", err)
	}
	next := later.AddDate(0, 0, 1)
	if _, _, err := env.Reading.DateRange(&next, &later); !errors.Is(err, reading.ErrInvalid) {
		t.Errorf("start after end accepted: %v", err)
	}
}

func linkedBook(t *testing.T, env *readingtest.Env, pages int) (catalog.Item, reading.Document) {
	t.Helper()
	env.HC.Add(readingtest.Book{ID: "1", Title: "Dune", Author: "Frank Herbert", Pages: pages, Synopsis: "Spice."})
	item, err := env.Catalog.EnsureItem(ctx, "book", "hardcover", "1")
	if err != nil {
		t.Fatal(err)
	}
	doc, _, err := env.Reading.GetOrCreateDocument(ctx, env.User, "h1", reading.DocFields{Title: "Dune", Authors: "Frank Herbert", TotalPages: ptr(int64(300))})
	if err != nil {
		t.Fatal(err)
	}
	return item, doc
}

func TestLinkSyncsProgressWithNeverLowerRule(t *testing.T) {
	env := readingtest.NewEnv(t, t0)
	item, doc := linkedBook(t, env, 500)
	// KOReader reports page 150 of 300: 50 %.
	if _, err := env.Reading.IngestKOReader(ctx, env.User, reading.KOBook{MD5: "h1", TotalPages: ptr(int64(300)), PageStats: []reading.PageStat{stat(150, t0.Add(-time.Hour), 60)}}, "Kindle"); err != nil {
		t.Fatal(err)
	}
	if _, err := env.Library.CurrentEntry(ctx, env.User, item.ID); !errors.Is(err, library.ErrNoEntry) {
		t.Fatalf("unlinked document created an entry: %v", err)
	}
	if _, err := env.Reading.LinkDocument(ctx, doc, item); err != nil {
		t.Fatal(err)
	}
	entry, err := env.Library.CurrentEntry(ctx, env.User, item.ID)
	if err != nil || entry.Status != library.InProgress || entry.Progress != 250 || !entry.StartedAt.Valid {
		t.Fatalf("after link: %+v %v", entry, err)
	}
	rows, _ := env.Reading.ListSessions(ctx, env.User, t0.AddDate(0, 0, -1), t0, 10)
	if len(rows) != 1 || rows[0].Session.ItemID.Int64 != item.ID {
		t.Errorf("sessions not backfilled with the item: %+v", rows)
	}

	// KOSync says 20 %: progress never goes down.
	if _, err := env.Reading.UpdateProgress(ctx, env.User, "H1", "xp://1", 0.2, "Kindle", "dev"); err != nil {
		t.Fatal(err)
	}
	if entry, _ = env.Library.CurrentEntry(ctx, env.User, item.ID); entry.Progress != 250 {
		t.Errorf("progress lowered to %d", entry.Progress)
	}
	// 60 %: moves up.
	env.Reading.UpdateProgress(ctx, env.User, "h1", "xp://2", 0.6, "Kindle", "dev")
	if entry, _ = env.Library.CurrentEntry(ctx, env.User, item.ID); entry.Progress != 300 {
		t.Errorf("progress = %d, want 300", entry.Progress)
	}
	// Finished (>= 99 %): completes at the edition's page count.
	env.Reading.UpdateProgress(ctx, env.User, "h1", "xp://3", 0.995, "Kindle", "dev")
	entry, _ = env.Library.CurrentEntry(ctx, env.User, item.ID)
	if entry.Status != library.Completed || entry.Progress != 500 {
		t.Fatalf("completed? %+v", entry)
	}
	// Re-opening the book doesn't regress a finished entry.
	env.Reading.UpdateProgress(ctx, env.User, "h1", "xp://0", 0.01, "Kindle", "dev")
	entry, _ = env.Library.CurrentEntry(ctx, env.User, item.ID)
	if entry.Status != library.Completed || entry.Progress != 500 {
		t.Errorf("finished entry regressed: %+v", entry)
	}
}

func TestKOReaderSyncMovesPlanningBookToInProgress(t *testing.T) {
	env := readingtest.NewEnv(t, t0)
	item, doc := linkedBook(t, env, 400)
	if _, err := env.Library.Track(ctx, env.User, item.ID, library.Planning, library.SourceManual, t0); err != nil {
		t.Fatal(err)
	}
	doc.ItemID.Valid = true
	if _, err := env.Reading.LinkDocument(ctx, doc, item); err != nil {
		t.Fatal(err)
	}
	if _, err := env.Reading.IngestKOReader(ctx, env.User, reading.KOBook{MD5: "h1", TotalPages: ptr(int64(300)), PageStats: []reading.PageStat{stat(3, t0.Add(-time.Hour), 60)}}, "Kindle"); err != nil {
		t.Fatal(err)
	}
	entry, _ := env.Library.CurrentEntry(ctx, env.User, item.ID)
	if entry.Status != library.InProgress || entry.Progress != 4 { // 3/300 of 400
		t.Errorf("entry = %+v", entry)
	}
}

func TestMergeRollsUpAndIgnoreHides(t *testing.T) {
	env := readingtest.NewEnv(t, t0)
	mk := func(md5 string, page int64, secs int64) reading.Document {
		res, err := env.Reading.IngestKOReader(ctx, env.User, reading.KOBook{MD5: md5, Title: "Dune", PageStats: []reading.PageStat{stat(page, t0.Add(-time.Hour), secs)}}, "")
		if err != nil {
			t.Fatal(err)
		}
		return res.Document
	}
	a, b, c := mk("aaa", 1, 600), mk("bbb", 2, 600), mk("ccc", 3, 60)

	if _, err := env.Reading.Merge(ctx, a, a.ID); !errors.Is(err, reading.ErrInvalid) {
		t.Errorf("self merge: %v", err)
	}
	if _, err := env.Reading.Merge(ctx, a, 9999); !errors.Is(err, reading.ErrNotFound) {
		t.Errorf("missing target: %v", err)
	}
	if _, err := env.Reading.Merge(ctx, b, a.ID); err != nil {
		t.Fatal(err)
	}
	// c merges into b, which is merged into a: it follows to the root.
	merged, err := env.Reading.Merge(ctx, c, b.ID)
	if err != nil || merged.MergedInto.Int64 != a.ID {
		t.Fatalf("chain: %+v %v", merged, err)
	}
	rows, err := env.Reading.Summary(ctx, env.User)
	if err != nil || len(rows) != 1 || rows[0].Document.ID != a.ID || rows[0].Pages != 3 || rows[0].Minutes != 21 || rows[0].Sessions != 3 {
		t.Fatalf("summary = %+v %v", rows, err)
	}
	sessions, _ := env.Reading.ListSessions(ctx, env.User, t0.AddDate(0, 0, -1), t0, 10)
	for _, s := range sessions {
		if s.DocumentID.Int64 != a.ID {
			t.Errorf("session reports document %d, want %d", s.DocumentID.Int64, a.ID)
		}
	}
	// Merging the root into another repoints what was merged into it.
	d := mk("ddd", 4, 60)
	if _, err := env.Reading.Merge(ctx, a, d.ID); err != nil {
		t.Fatal(err)
	}
	rows, _ = env.Reading.Summary(ctx, env.User)
	if len(rows) != 1 || rows[0].Document.ID != d.ID || rows[0].Sessions != 4 {
		t.Errorf("after re-root: %+v", rows)
	}

	d, err = env.Reading.Ignore(ctx, d, true)
	if err != nil || d.Ignored != 1 {
		t.Fatalf("ignore: %+v %v", d, err)
	}
	if _, ok, _ := env.Reading.AutoLink(ctx, d); ok {
		t.Error("ignored document was auto-linked")
	}
}

func TestMergeCarriesTheLinkedBook(t *testing.T) {
	env := readingtest.NewEnv(t, t0)
	item, doc := linkedBook(t, env, 500)
	other, _, _ := env.Reading.GetOrCreateDocument(ctx, env.User, "h2", reading.DocFields{Title: "Dune (other file)"})
	if _, err := env.Reading.LinkDocument(ctx, doc, item); err != nil {
		t.Fatal(err)
	}
	// An unlinked document merged into a linked one counts for its book.
	if _, err := env.Reading.Merge(ctx, other, doc.ID); err != nil {
		t.Fatal(err)
	}
	// A linked document merged into an unlinked target passes its book on.
	t2, _, _ := env.Reading.GetOrCreateDocument(ctx, env.User, "h3", reading.DocFields{Title: "Dune again"})
	d1, _ := env.Reading.Get(ctx, env.User, doc.ID)
	if _, err := env.Reading.Merge(ctx, d1, t2.ID); err != nil {
		t.Fatal(err)
	}
	got, _ := env.Reading.Get(ctx, env.User, t2.ID)
	if got.ItemID.Int64 != item.ID {
		t.Errorf("target item = %v, want %d", got.ItemID, item.ID)
	}
}

func TestAutoLinkLinksAndRecordsAttempts(t *testing.T) {
	env := readingtest.NewEnv(t, t0)
	env.HC.Add(readingtest.Book{ID: "1", Title: "Dune", Author: "Frank Herbert", Pages: 500})
	good, _, _ := env.Reading.GetOrCreateDocument(ctx, env.User, "g", reading.DocFields{Title: "Herbert, Frank - Dune 01 - Dune", Authors: "Herbert, Frank"})
	bad, _, _ := env.Reading.GetOrCreateDocument(ctx, env.User, "b", reading.DocFields{Title: "Some Unknown Book", Authors: "Nobody"})
	stock, _, _ := env.Reading.GetOrCreateDocument(ctx, env.User, "s", reading.DocFields{Title: "KOReader Quickstart Guide", Authors: "N/A"})

	linked, tried, err := env.Reading.AutoLinkPending(ctx, env.User)
	if err != nil || linked != 1 || tried != 3 {
		t.Fatalf("linked=%d tried=%d err=%v", linked, tried, err)
	}
	g, _ := env.Reading.Get(ctx, env.User, good.ID)
	b, _ := env.Reading.Get(ctx, env.User, bad.ID)
	if !g.ItemID.Valid || b.ItemID.Valid || !b.LinkCheckedAt.Valid || b.Ignored != 0 {
		t.Errorf("good=%+v bad=%+v", g, b)
	}
	// The stock guide is ignored on sight, never linked.
	if st, _ := env.Reading.Get(ctx, env.User, stock.ID); st.Ignored != 1 || st.ItemID.Valid {
		t.Errorf("stock document = %+v", st)
	}
	// Tried documents aren't retried.
	calls := env.HC.Calls
	if _, tried, _ := env.Reading.AutoLinkPending(ctx, env.User); tried != 0 || env.HC.Calls != calls {
		t.Errorf("retried: tried=%d calls %d -> %d", tried, calls, env.HC.Calls)
	}
	// Provider outage: not recorded as tried.
	env.HC.Err, env.OL.Err = errors.New("down"), errors.New("down")
	c, _, _ := env.Reading.GetOrCreateDocument(ctx, env.User, "c", reading.DocFields{Title: "Dune", Authors: "Frank Herbert"})
	if _, ok, err := env.Reading.AutoLink(ctx, c); ok || !errors.Is(err, reading.ErrProvider) {
		t.Errorf("outage: ok=%v err=%v", ok, err)
	}
	if c, _ = env.Reading.Get(ctx, env.User, c.ID); c.LinkCheckedAt.Valid {
		t.Error("outage marked the document as checked")
	}
}

// bookEntry makes an entry of a Hardcover book and returns its id.
func bookEntry(t *testing.T, env *readingtest.Env, b readingtest.Book, status string) (int64, catalog.Item) {
	t.Helper()
	env.HC.Add(b)
	item, err := env.Catalog.EnsureItem(ctx, "book", "hardcover", b.ID)
	if err != nil {
		t.Fatal(err)
	}
	e, err := env.Library.Track(ctx, env.User, item.ID, status, library.SourceManual, t0)
	if err != nil {
		t.Fatal(err)
	}
	return e.ID, item
}

func TestNotesSeedPreviewFromSynopsis(t *testing.T) {
	env := readingtest.NewEnv(t, t0)
	id, _ := bookEntry(t, env, readingtest.Book{ID: "1", Title: "Dune", Synopsis: "A desert planet.", Pages: 500}, library.InProgress)
	n, err := env.Reading.GetNotes(ctx, env.User, id)
	if err != nil {
		t.Fatal(err)
	}
	if n.Preview != "A desert planet." || n.Purpose != nil || !n.PromptPredict || n.UpdatedAt == "" {
		t.Errorf("notes = %+v", n)
	}
	// Seeded once: later changes to the synopsis don't touch the saved preview.
	env.HC.Books[0].Synopsis = "Changed"
	if n2, _ := env.Reading.GetNotes(ctx, env.User, id); n2.Preview != "A desert planet." {
		t.Errorf("preview re-seeded: %q", n2.Preview)
	}
}

func TestNotesSeedPreviewWithOpenLibraryContents(t *testing.T) {
	env := readingtest.NewEnv(t, t0)
	env.OL.Add(readingtest.Book{ID: "OL1M", Title: "Deep Work", Synopsis: "Focus.", Pages: 300, TOC: []string{"Part 1", "Part 2"}})
	item, err := env.Catalog.EnsureItem(ctx, "book", "openlibrary", "OL1M")
	if err != nil {
		t.Fatal(err)
	}
	e, _ := env.Library.Track(ctx, env.User, item.ID, library.Planning, library.SourceManual, t0)
	n, err := env.Reading.GetNotes(ctx, env.User, e.ID)
	if err != nil {
		t.Fatal(err)
	}
	want := "Focus.\n\nContents:\n  Part 1\n  Part 2"
	if n.Preview != want {
		t.Errorf("preview = %q, want %q", n.Preview, want)
	}
	if n.PromptPredict {
		t.Error("prompt_predict for a Planning book")
	}
}

func TestNotesSeedRetriesAfterOpenLibraryFailure(t *testing.T) {
	env := readingtest.NewEnv(t, t0)
	env.OL.Add(readingtest.Book{ID: "OL1M", Title: "Deep Work", Synopsis: "Focus.", Pages: 300, TOC: []string{"Part 1"}})
	item, _ := env.Catalog.EnsureItem(ctx, "book", "openlibrary", "OL1M")
	e, _ := env.Library.Track(ctx, env.User, item.ID, library.InProgress, library.SourceManual, t0)
	env.OL.Err = errors.New("down")
	n, err := env.Reading.GetNotes(ctx, env.User, e.ID)
	if err != nil || n.Preview != "Focus." {
		t.Fatalf("degraded preview = %+v %v", n, err)
	}
	env.OL.Err = nil
	n, _ = env.Reading.GetNotes(ctx, env.User, e.ID)
	if !strings.Contains(n.Preview, "Part 1") {
		t.Errorf("preview not retried: %q", n.Preview)
	}
}

func TestNotesUpdateAndPromptPredict(t *testing.T) {
	env := readingtest.NewEnv(t, t0)
	id, _ := bookEntry(t, env, readingtest.Book{ID: "1", Title: "Dune", Synopsis: "Spice.", Pages: 500}, library.InProgress)

	n, err := env.Reading.UpdateNotes(ctx, env.User, id, reading.NotesUpdate{Predict: ptr("Paul wins")})
	if err != nil || n.Predict != "Paul wins" || n.Preview != "Spice." || n.Purpose != nil || n.PromptPredict {
		t.Fatalf("predict only: %+v %v", n, err)
	}
	n, err = env.Reading.UpdateNotes(ctx, env.User, id, reading.NotesUpdate{PurposeSet: true, Purpose: ptr("entertain"), PurposeNote: ptr("fun")})
	if err != nil || *n.Purpose != "entertain" || n.PurposeNote != "fun" || n.Predict != "Paul wins" || n.PromptPredict {
		t.Fatalf("purpose: %+v %v", n, err)
	}
	n, err = env.Reading.UpdateNotes(ctx, env.User, id, reading.NotesUpdate{PurposeSet: true, Purpose: nil, Predict: ptr(""), Reflect: ptr("Great")})
	if err != nil || n.Purpose != nil || n.Reflect != "Great" || !n.PromptPredict {
		t.Fatalf("clear: %+v %v", n, err)
	}

	for _, bad := range []reading.NotesUpdate{
		{PurposeSet: true, Purpose: ptr("inform")},
		{Reflect: ptr(strings.Repeat("x", reading.MaxNoteLength+1))},
	} {
		if _, err := env.Reading.UpdateNotes(ctx, env.User, id, bad); !errors.Is(err, reading.ErrInvalid) {
			t.Errorf("accepted %+v: %v", bad, err)
		}
	}
}

func TestNotesRejectOtherUsersEntriesAndNonBooks(t *testing.T) {
	env := readingtest.NewEnv(t, t0)
	id, _ := bookEntry(t, env, readingtest.Book{ID: "1", Title: "Dune", Pages: 500}, library.InProgress)
	other := env.Env.User(t, "other")
	if _, err := env.Reading.GetNotes(ctx, other, id); !errors.Is(err, reading.ErrNotFound) {
		t.Errorf("other user's entry: %v", err)
	}
	if _, err := env.Reading.GetNotes(ctx, env.User, 9999); !errors.Is(err, reading.ErrNotFound) {
		t.Errorf("missing entry: %v", err)
	}
	env.TMDB.AddMovie("5", "A Movie", "2020-01-01")
	movie, _ := env.Catalog.EnsureItem(ctx, "movie", "tmdb", "5")
	e, _ := env.Library.Track(ctx, env.User, movie.ID, library.Planning, library.SourceManual, t0)
	if _, err := env.Reading.GetNotes(ctx, env.User, e.ID); !errors.Is(err, reading.ErrNotFound) {
		t.Errorf("movie entry: %v", err)
	}
}

func TestCreateSession(t *testing.T) {
	env := readingtest.NewEnv(t, t0)
	_, item := bookEntry(t, env, readingtest.Book{ID: "1", Title: "Dune", Pages: 500}, library.InProgress)
	s, err := env.Reading.CreateSession(ctx, env.User, reading.NewSession{Source: "manual", ItemID: &item.ID, StartedAt: t0, EndedAt: t0.Add(90 * time.Second), PagesRead: 4})
	if err != nil || s.Title != "Dune" || s.DurationSeconds != 90 {
		t.Fatalf("session = %+v %v", s, err)
	}
	if _, err := env.Reading.CreateSession(ctx, env.User, reading.NewSession{Source: "bogus", StartedAt: t0, EndedAt: t0}); !errors.Is(err, reading.ErrInvalid) {
		t.Errorf("bogus source: %v", err)
	}
	if _, err := env.Reading.CreateSession(ctx, env.User, reading.NewSession{Source: "manual", StartedAt: t0, EndedAt: t0.Add(-time.Second)}); !errors.Is(err, reading.ErrInvalid) {
		t.Errorf("negative duration: %v", err)
	}
	missing := int64(9999)
	if _, err := env.Reading.CreateSession(ctx, env.User, reading.NewSession{Source: "manual", ItemID: &missing, StartedAt: t0, EndedAt: t0}); !errors.Is(err, reading.ErrNotFound) {
		t.Errorf("missing item: %v", err)
	}
}

func TestAutoLinkAllUsersLinksUntriedDocuments(t *testing.T) {
	env := readingtest.NewEnv(t, t0)
	env.HC.Add(readingtest.Book{ID: "7", Title: "Project Hail Mary", Author: "Andy Weir", Pages: 480})
	d, _, _ := env.Reading.GetOrCreateDocument(ctx, env.User, "phm", reading.DocFields{Title: "Project Hail Mary", Authors: "Andy Weir"})
	if err := env.Reading.AutoLinkAllUsers(ctx); err != nil {
		t.Fatal(err)
	}
	if d, _ = env.Reading.Get(ctx, env.User, d.ID); !d.ItemID.Valid {
		t.Fatalf("document not linked: %+v", d)
	}
}

// Linking a document finished long ago dates the finish to the last reading
// session, not to the moment of linking.
func TestLinkOldFinishedDocumentKeepsReadingDates(t *testing.T) {
	env := readingtest.NewEnv(t, t0)
	env.HC.Add(readingtest.Book{ID: "9", Title: "Golden Son", Author: "Pierce Brown", Pages: 400})
	start := time.Date(2025, 3, 1, 20, 0, 0, 0, time.UTC)
	end := time.Date(2025, 3, 9, 21, 0, 0, 0, time.UTC)
	res, err := env.Reading.IngestKOReader(ctx, env.User, reading.KOBook{MD5: "old", Title: "Golden Son", Authors: "Pierce Brown", TotalPages: ptr(int64(400)),
		PageStats: []reading.PageStat{stat(1, start, 600), stat(400, end, 600)}}, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := env.Reading.LinkToBook(ctx, res.Document, "hardcover", "9"); err != nil {
		t.Fatal(err)
	}
	item, err := env.Catalog.EnsureItem(ctx, "book", "hardcover", "9")
	if err != nil {
		t.Fatal(err)
	}
	e, err := env.Library.CurrentEntry(ctx, env.User, item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if e.Status != library.Completed || !strings.HasPrefix(e.FinishedAt.String, "2025-03-09") || !strings.HasPrefix(e.StartedAt.String, "2025-03-01") {
		t.Fatalf("status=%s started=%s finished=%s", e.Status, e.StartedAt.String, e.FinishedAt.String)
	}
}

// A one-page open (an accidental tap on a book) isn't reading: it makes no
// session, while the page event itself is kept.
func TestOnePageOpenIsNotASession(t *testing.T) {
	env := readingtest.NewEnv(t, t0)
	reading.MinSessionPages = 2
	day := time.Date(2026, 11, 20, 20, 0, 0, 0, time.UTC)
	res, err := env.Reading.IngestKOReader(ctx, env.User, reading.KOBook{MD5: "x", Title: "Dune", Authors: "Frank Herbert", TotalPages: ptr(int64(500)),
		PageStats: []reading.PageStat{
			stat(40, day, 30),                                                                      // accidental open
			stat(41, day.Add(2*time.Hour), 120), stat(42, day.Add(2*time.Hour+2*time.Minute), 120), // real reading
		}}, "")
	if err != nil {
		t.Fatal(err)
	}
	days, err := env.Reading.Journal(ctx, env.User, day, day)
	if err != nil {
		t.Fatal(err)
	}
	if len(days) != 1 || days[0].Pages != 2 || days[0].Minutes != 4 {
		t.Fatalf("journal = %+v", days)
	}
	if m, err := env.Q.MaxDocumentPage(ctx, res.Document.ID); err != nil || m.Page != 42 {
		t.Fatalf("page events: %+v %v", m, err)
	}
}
