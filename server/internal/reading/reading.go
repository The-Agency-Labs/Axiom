// Package reading ports src/reading: KOReader documents and page events,
// reading sessions, daily stats and streaks, KOSync progress, linking
// documents to library books (with a conservative auto-linker), and the
// Triple P + Reflect notes of a read-through.
package reading

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/the-agency-labs/axiom/server/internal/catalog"
	"github.com/the-agency-labs/axiom/server/internal/db"
	"github.com/the-agency-labs/axiom/server/internal/library"
)

// MinSessionPages: a KOReader session must cover at least this many pages to
// count as reading; a single page is an accidental open. The page events are
// kept either way.
var MinSessionPages = 2

// SessionGap: a pause longer than this between page turns starts a new session.
const SessionGap = 10 * time.Minute

// MinPageSeconds: KOReader ignores page views shorter than ~5s by default; so do we.
const MinPageSeconds = 5

// CompletePercentage: KOReader rarely reports exactly 1.0 on the last page.
const CompletePercentage = 0.99

// SourceKOReader is the library source and session source of device syncs.
const SourceKOReader = "koreader"

// Session sources (reading.SessionSource).
const (
	SourceManual    = "manual"
	SourcePaperback = "paperback"
)

// ValidSessionSource reports whether a session source is known.
func ValidSessionSource(s string) bool {
	return s == SourceKOReader || s == SourcePaperback || s == SourceManual
}

// Errors.
var (
	ErrNotFound    = errors.New("reading: not found")
	ErrInvalid     = errors.New("reading: invalid input")
	ErrProvider    = errors.New("reading: provider unavailable")
	ErrUnsupported = errors.New("reading: unsupported source")
)

// Document is a row of reading_documents.
type Document = db.ReadingDocument

// Session is a row of reading_sessions.
type Session = db.ReadingSession

// Service is the reading domain. Create it with New.
type Service struct {
	Conn    *sql.DB
	Q       *db.Queries
	Catalog *catalog.Catalog
	Library *library.Service
	// Now and Loc are the clock and the zone days are read in.
	Now func() time.Time
	Loc *time.Location
}

// New returns a Service. Library and catalog supply books and provider data.
func New(conn *sql.DB, q *db.Queries, cat *catalog.Catalog, lib *library.Service) *Service {
	s := &Service{Conn: conn, Q: q, Catalog: cat, Library: lib, Now: func() time.Time { return time.Now().UTC() }, Loc: time.UTC}
	if lib != nil {
		if lib.Now != nil {
			s.Now = lib.Now
		}
		if lib.Loc != nil {
			s.Loc = lib.Loc
		}
	}
	return s
}

func (s *Service) now() time.Time { return s.Now().UTC() }

// Stamp formats a time the way the tables store it.
func Stamp(t time.Time) string { return t.UTC().Format(time.RFC3339) }

func nstamp(t time.Time) sql.NullString { return sql.NullString{String: Stamp(t), Valid: true} }

func parseStamp(v string) time.Time {
	t, _ := time.Parse(time.RFC3339, v)
	return t
}

func nullInt(n int64, valid bool) sql.NullInt64 { return sql.NullInt64{Int64: n, Valid: valid} }

func (s *Service) tx(ctx context.Context, fn func(q *db.Queries) error) error {
	tx, err := s.Conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := fn(s.Q.WithTx(tx)); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

// DocFields are the metadata a device reports for a document; empty values
// never overwrite what is stored.
type DocFields struct {
	Title      string
	Authors    string
	TotalPages *int64
}

// GetOrCreateDocument returns the user's document for a hash (lowercased),
// filling in any new metadata.
func (s *Service) GetOrCreateDocument(ctx context.Context, userID int64, hash string, f DocFields) (Document, bool, error) {
	return getOrCreateDocument(ctx, s.Q, userID, hash, f, s.now())
}

func getOrCreateDocument(ctx context.Context, q *db.Queries, userID int64, hash string, f DocFields, now time.Time) (Document, bool, error) {
	hash = strings.ToLower(hash)
	doc, err := q.GetReadingDocumentByHash(ctx, db.GetReadingDocumentByHashParams{UserID: userID, DocumentHash: hash})
	if errors.Is(err, sql.ErrNoRows) {
		var tp sql.NullInt64
		if f.TotalPages != nil && *f.TotalPages > 0 {
			tp = nullInt(*f.TotalPages, true)
		}
		doc, err = q.InsertReadingDocument(ctx, db.InsertReadingDocumentParams{
			UserID: userID, DocumentHash: hash, Title: f.Title, Authors: f.Authors, TotalPages: tp,
			CreatedAt: Stamp(now), UpdatedAt: Stamp(now),
		})
		return doc, true, err
	}
	if err != nil {
		return doc, false, err
	}
	changed := false
	if f.Title != "" && f.Title != doc.Title {
		doc.Title, changed = f.Title, true
	}
	if f.Authors != "" && f.Authors != doc.Authors {
		doc.Authors, changed = f.Authors, true
	}
	if f.TotalPages != nil && *f.TotalPages > 0 && (!doc.TotalPages.Valid || doc.TotalPages.Int64 != *f.TotalPages) {
		doc.TotalPages, changed = nullInt(*f.TotalPages, true), true
	}
	if changed {
		doc.UpdatedAt = Stamp(now)
		err = q.UpdateReadingDocumentMeta(ctx, db.UpdateReadingDocumentMetaParams{
			Title: doc.Title, Authors: doc.Authors, TotalPages: doc.TotalPages, UpdatedAt: doc.UpdatedAt, ID: doc.ID,
		})
	}
	return doc, false, err
}

// PageStat is one row of KOReader's page_stat_data table.
type PageStat struct {
	Page       int64
	StartTime  int64 // epoch seconds
	Duration   int64
	TotalPages *int64
}

// KOBook is one book of a KOReader upload.
type KOBook struct {
	MD5        string
	Title      string
	Authors    string
	TotalPages *int64
	PageStats  []PageStat
}

// IngestResult describes what an upload did to one document.
type IngestResult struct {
	Document Document
	Added    int64
	Created  bool
}

// IngestKOReader stores one book's page events from a KOReader statistics
// upload (safe to resend the same rows) and rebuilds its sessions when rows
// were new. A linked book's library progress follows.
func (s *Service) IngestKOReader(ctx context.Context, userID int64, b KOBook, device string) (IngestResult, error) {
	var res IngestResult
	err := s.tx(ctx, func(q *db.Queries) error {
		doc, created, err := getOrCreateDocument(ctx, q, userID, b.MD5, DocFields{Title: b.Title, Authors: b.Authors, TotalPages: b.TotalPages}, s.now())
		if err != nil {
			return err
		}
		res.Document, res.Created = doc, created
		for _, st := range b.PageStats {
			if st.Duration < MinPageSeconds {
				continue
			}
			total := b.TotalPages
			if st.TotalPages != nil && *st.TotalPages > 0 {
				total = st.TotalPages
			}
			var tp sql.NullInt64
			if total != nil && *total > 0 {
				tp = nullInt(*total, true)
			}
			n, err := q.InsertPageEvent(ctx, db.InsertPageEventParams{
				DocumentID: doc.ID, Page: st.Page, StartTime: Stamp(time.Unix(st.StartTime, 0)),
				DurationSeconds: st.Duration, TotalPages: tp,
			})
			if err != nil {
				return err
			}
			res.Added += n
		}
		if res.Added > 0 {
			return rebuildSessions(ctx, q, doc, device)
		}
		return nil
	})
	if err != nil {
		return res, err
	}
	// The layout may have changed even when no row is new.
	if err := s.MatchDevicePages(ctx, res.Document); err != nil {
		return res, err
	}
	if res.Added > 0 {
		if err := s.SyncLibraryProgress(ctx, res.Document); err != nil {
			return res, err
		}
		// Re-read: the document may have been linked meanwhile.
		if d, err := s.Q.GetReadingDocument(ctx, db.GetReadingDocumentParams{ID: res.Document.ID, UserID: userID}); err == nil {
			res.Document = d
		}
	}
	return res, nil
}

// groupEvents splits time-ordered page events into sessions separated by SessionGap.
func groupEvents(events []db.PageEvent) [][]db.PageEvent {
	var groups [][]db.PageEvent
	var cur []db.PageEvent
	var curEnd time.Time
	for _, e := range events {
		start := parseStamp(e.StartTime)
		if len(cur) > 0 && start.Sub(curEnd) > SessionGap {
			groups = append(groups, cur)
			cur = nil
		}
		cur = append(cur, e)
		end := start.Add(time.Duration(e.DurationSeconds) * time.Second)
		if curEnd.IsZero() || end.After(curEnd) {
			curEnd = end
		}
	}
	if len(cur) > 0 {
		groups = append(groups, cur)
	}
	return groups
}

// rebuildSessions recreates a document's KOReader sessions from its page
// events, leaving out one-page opens (MinSessionPages).
func rebuildSessions(ctx context.Context, q *db.Queries, doc Document, device string) error {
	if err := q.DeleteKOReaderSessions(ctx, sql.NullInt64{Int64: doc.ID, Valid: true}); err != nil {
		return err
	}
	events, err := q.ListPageEvents(ctx, doc.ID)
	if err != nil {
		return err
	}
	if device == "" {
		device = doc.Device
	}
	for _, g := range groupEvents(events) {
		pages := map[int64]bool{}
		var seconds int64
		// The range is the lowest to the highest page seen: flipping back and
		// returning must not collapse it to "from 188 to 188".
		low, high := g[0].Page, g[0].Page
		for _, e := range g {
			low, high = min(low, e.Page), max(high, e.Page)
			pages[e.Page] = true
			seconds += e.DurationSeconds
		}
		if len(pages) < MinSessionPages {
			continue
		}
		last := g[len(g)-1]
		_, err := q.InsertReadingSession(ctx, db.InsertReadingSessionParams{
			UserID: doc.UserID, Source: SourceKOReader, DocumentID: nullInt(doc.ID, true), ItemID: effectiveItemTx(ctx, q, doc),
			Title: doc.Title, StartedAt: g[0].StartTime,
			EndedAt:         Stamp(parseStamp(last.StartTime).Add(time.Duration(last.DurationSeconds) * time.Second)),
			DurationSeconds: seconds, PagesRead: int64(len(pages)),
			StartPage: nullInt(low, true), EndPage: nullInt(high, true), Device: device,
		})
		if err != nil {
			return err
		}
	}
	return nil
}

// effectiveItemTx is the item a document counts toward: its own, or the one
// of the document it was merged into.
func effectiveItemTx(ctx context.Context, q *db.Queries, doc Document) sql.NullInt64 {
	if doc.ItemID.Valid {
		return doc.ItemID
	}
	if doc.MergedInto.Valid {
		if t, err := q.GetReadingDocument(ctx, db.GetReadingDocumentParams{ID: doc.MergedInto.Int64, UserID: doc.UserID}); err == nil {
			return t.ItemID
		}
	}
	return sql.NullInt64{}
}

// UpdateProgress records a KOSync position and pushes it to the library.
// It returns the stored timestamp.
func (s *Service) UpdateProgress(ctx context.Context, userID int64, hash, progress string, percentage float64, device, deviceID string) (Document, error) {
	doc, _, err := s.GetOrCreateDocument(ctx, userID, hash, DocFields{})
	if err != nil {
		return doc, err
	}
	now := s.now()
	doc.Progress, doc.Percentage = progress, sql.NullFloat64{Float64: percentage, Valid: true}
	doc.Device, doc.DeviceID, doc.ProgressUpdatedAt, doc.UpdatedAt = device, deviceID, nstamp(now), Stamp(now)
	err = s.Q.UpdateReadingDocumentProgress(ctx, db.UpdateReadingDocumentProgressParams{
		Progress: doc.Progress, Percentage: doc.Percentage, Device: device, DeviceID: deviceID,
		ProgressUpdatedAt: doc.ProgressUpdatedAt, UpdatedAt: doc.UpdatedAt, ID: doc.ID,
	})
	if err != nil {
		return doc, err
	}
	return doc, s.SyncLibraryProgress(ctx, doc)
}

// GetProgress returns the stored KOSync position of a document, or false
// when none was reported.
func (s *Service) GetProgress(ctx context.Context, userID int64, hash string) (Document, bool, error) {
	doc, err := s.Q.GetReadingDocumentByHash(ctx, db.GetReadingDocumentByHashParams{UserID: userID, DocumentHash: strings.ToLower(hash)})
	if errors.Is(err, sql.ErrNoRows) || (err == nil && !doc.ProgressUpdatedAt.Valid) {
		return doc, false, nil
	}
	return doc, err == nil, err
}

// root follows merged_into to the document a document's reading counts toward.
func (s *Service) root(ctx context.Context, doc Document) (Document, error) {
	for i := 0; i < 8 && doc.MergedInto.Valid; i++ {
		next, err := s.Q.GetReadingDocument(ctx, db.GetReadingDocumentParams{ID: doc.MergedInto.Int64, UserID: doc.UserID})
		if err != nil {
			return doc, err
		}
		doc = next
	}
	return doc, nil
}

// Get returns a user's document by id.
func (s *Service) Get(ctx context.Context, userID, id int64) (Document, error) {
	doc, err := s.Q.GetReadingDocument(ctx, db.GetReadingDocumentParams{ID: id, UserID: userID})
	if errors.Is(err, sql.ErrNoRows) {
		return doc, ErrNotFound
	}
	return doc, err
}

// MatchDevicePages gives the book a document is linked to the page count the
// device uses for it, so the library's pages are the ones on the reader's
// screen. It does nothing for an unlinked document.
func (s *Service) MatchDevicePages(ctx context.Context, doc Document) error {
	root, err := s.root(ctx, doc)
	if err != nil || !root.ItemID.Valid {
		return err
	}
	return s.MatchItemPages(ctx, root.ItemID.Int64)
}

// MatchItemPages is MatchDevicePages by book.
func (s *Service) MatchItemPages(ctx context.Context, itemID int64) error {
	if s.Library == nil {
		return nil
	}
	pages, err := s.Q.ItemDevicePages(ctx, itemID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	return s.Library.SetPageCount(ctx, itemID, pages.Int64)
}

// MatchAllDevicePages applies MatchItemPages to every linked book.
func (s *Service) MatchAllDevicePages(ctx context.Context) error {
	ids, err := s.Q.ListDevicePagedItems(ctx)
	if err != nil {
		return err
	}
	for _, id := range ids {
		if err := s.MatchItemPages(ctx, id); err != nil {
			return err
		}
	}
	return nil
}

// SyncLibraryProgress mirrors a linked document's position onto the user's
// Book entry (ports sync_library_progress). The book takes the device's page
// count first, then the position in those pages: the KOSync percentage, or
// without one the furthest point KOReader's page events reached. A new book starts In progress; a Planning
// one moves to In progress; Completed entries are left alone, and the
// library's never-lower rule applies because the source is "koreader".
func (s *Service) SyncLibraryProgress(ctx context.Context, doc Document) error {
	if s.Library == nil {
		return nil
	}
	root, err := s.root(ctx, doc)
	if err != nil || !root.ItemID.Valid {
		return err
	}
	if err := s.MatchItemPages(ctx, root.ItemID.Int64); err != nil {
		return err
	}
	item, err := s.Catalog.GetItem(ctx, root.ItemID.Int64)
	if err != nil || item.Kind != catalog.KindBook {
		return err
	}
	userID := doc.UserID

	entry, err := s.Library.CurrentEntry(ctx, userID, item.ID)
	hasEntry := err == nil
	if err != nil && !errors.Is(err, library.ErrNoEntry) {
		return err
	}
	if hasEntry && entry.Status == library.Completed {
		return nil
	}

	at := s.now()
	if first, err := s.Q.FirstDocumentSessionStart(ctx, sql.NullInt64{Int64: root.ID, Valid: true}); err == nil {
		at = parseStamp(first)
	}
	if !hasEntry {
		if _, err := s.Library.Track(ctx, userID, item.ID, library.InProgress, SourceKOReader, at); err != nil {
			return err
		}
	} else if entry.Status == library.Planning {
		if err := s.Library.SetStatus(ctx, userID, item.ID, library.InProgress, SourceKOReader, at); err != nil {
			return err
		}
	}

	pct, deviceTotal := s.position(ctx, doc)
	total := item.Total.Int64
	if total <= 0 {
		total = deviceTotal
	}
	if pct < 0 || total <= 0 {
		return nil
	}
	progress := int(math.RoundToEven(pct * float64(total)))
	if pct >= CompletePercentage {
		progress = int(total)
	}
	return s.Library.SetProgress(ctx, userID, item.ID, progress, SourceKOReader, s.lastReadAt(ctx, root))
}

// lastReadAt is when the document was last read: its newest session end or
// KOSync position, whichever is later, never in the future. Progress (and a
// finish date) carries that time, so linking an old document doesn't date
// the reading to the moment it was linked.
func (s *Service) lastReadAt(ctx context.Context, doc Document) time.Time {
	now := s.now()
	var last time.Time
	if end, err := s.Q.LastDocumentSessionEnd(ctx, sql.NullInt64{Int64: doc.ID, Valid: true}); err == nil {
		last = parseStamp(end)
	}
	if doc.ProgressUpdatedAt.Valid {
		if t := parseStamp(doc.ProgressUpdatedAt.String); t.After(last) {
			last = t
		}
	}
	if last.IsZero() || last.After(now) {
		return now
	}
	return last
}

// position returns how far through the book a document is (0..1, -1 when
// unknown) and KOReader's page count for it.
func (s *Service) position(ctx context.Context, doc Document) (float64, int64) {
	deviceTotal := doc.TotalPages.Int64
	if doc.Percentage.Valid {
		return math.Max(0, math.Min(doc.Percentage.Float64, 1)), deviceTotal
	}
	furthest, err := s.Q.FurthestDocumentFraction(ctx, doc.ID)
	if err != nil || furthest <= 0 {
		return -1, deviceTotal
	}
	return math.Min(furthest, 1), deviceTotal
}

// LinkDocument links a document to a library item, backfills its sessions
// and syncs progress (ports link_document).
func (s *Service) LinkDocument(ctx context.Context, doc Document, item catalog.Item) (Document, error) {
	now := s.now()
	err := s.tx(ctx, func(q *db.Queries) error {
		if err := q.SetReadingDocumentItem(ctx, db.SetReadingDocumentItemParams{
			ItemID: nullInt(item.ID, true), LinkCheckedAt: nstamp(now), UpdatedAt: Stamp(now), ID: doc.ID,
		}); err != nil {
			return err
		}
		return q.SetDocumentSessionsItem(ctx, db.SetDocumentSessionsItemParams{ItemID: nullInt(item.ID, true), DocumentID: nullInt(doc.ID, true)})
	})
	if err != nil {
		return doc, err
	}
	doc.ItemID, doc.UpdatedAt = nullInt(item.ID, true), Stamp(now)
	return doc, s.SyncLibraryProgress(ctx, doc)
}

// Ignore hides a document from the unlinked list (or shows it again).
func (s *Service) Ignore(ctx context.Context, doc Document, ignored bool) (Document, error) {
	v := int64(0)
	if ignored {
		v = 1
	}
	now := Stamp(s.now())
	if err := s.Q.SetReadingDocumentIgnored(ctx, db.SetReadingDocumentIgnoredParams{Ignored: v, UpdatedAt: now, ID: doc.ID}); err != nil {
		return doc, err
	}
	doc.Ignored, doc.UpdatedAt = v, now
	return doc, nil
}

// Merge marks doc as another file of the same book as target: its sessions
// and totals roll up to the target. Merging into a merged document merges
// into its root; a linked document passes its book to an unlinked target.
func (s *Service) Merge(ctx context.Context, doc Document, targetID int64) (Document, error) {
	target, err := s.Get(ctx, doc.UserID, targetID)
	if err != nil {
		return doc, err
	}
	if target, err = s.root(ctx, target); err != nil {
		return doc, err
	}
	if target.ID == doc.ID {
		return doc, fmt.Errorf("%w: a document can't be merged into itself", ErrInvalid)
	}
	now := Stamp(s.now())
	err = s.tx(ctx, func(q *db.Queries) error {
		if err := q.RepointMergedDocuments(ctx, db.RepointMergedDocumentsParams{NewTarget: nullInt(target.ID, true), OldTarget: nullInt(doc.ID, true)}); err != nil {
			return err
		}
		if err := q.SetReadingDocumentMerged(ctx, db.SetReadingDocumentMergedParams{MergedInto: nullInt(target.ID, true), UpdatedAt: now, ID: doc.ID}); err != nil {
			return err
		}
		if !target.ItemID.Valid && doc.ItemID.Valid {
			if err := q.SetReadingDocumentItem(ctx, db.SetReadingDocumentItemParams{ItemID: doc.ItemID, LinkCheckedAt: nstamp(s.now()), UpdatedAt: now, ID: target.ID}); err != nil {
				return err
			}
			target.ItemID = doc.ItemID
		}
		return q.SetDocumentSessionsItem(ctx, db.SetDocumentSessionsItemParams{ItemID: target.ItemID, DocumentID: nullInt(target.ID, true)})
	})
	if err != nil {
		return doc, err
	}
	doc.MergedInto, doc.UpdatedAt = nullInt(target.ID, true), now
	if target.ItemID.Valid {
		_ = s.SyncLibraryProgress(ctx, doc)
	}
	return doc, nil
}

// Row is a document with the totals of its sessions (and those of the
// documents merged into it).
type Row struct {
	Document Document
	Pages    int64
	Minutes  int64
	Sessions int64
}

// Summary returns the user's documents with reading totals, newest first.
// Documents merged into another are folded into it and not listed.
func (s *Service) Summary(ctx context.Context, userID int64) ([]Row, error) {
	totals, err := s.Q.ReadingDocumentTotals(ctx, userID)
	if err != nil {
		return nil, err
	}
	docs, err := s.Q.ListReadingDocuments(ctx, userID)
	if err != nil {
		return nil, err
	}
	type agg struct{ pages, seconds, sessions int64 }
	byDoc := map[int64]agg{}
	for _, t := range totals {
		byDoc[t.DocumentID.Int64] = agg{t.Pages, t.Seconds, t.Sessions}
	}
	byID := map[int64]Document{}
	for _, d := range docs {
		byID[d.ID] = d
	}
	rootOf := func(d Document) int64 {
		for i := 0; i < 8 && d.MergedInto.Valid; i++ {
			next, ok := byID[d.MergedInto.Int64]
			if !ok {
				break
			}
			d = next
		}
		return d.ID
	}
	sum := map[int64]agg{}
	for _, d := range docs {
		r := rootOf(d)
		a := sum[r]
		b := byDoc[d.ID]
		sum[r] = agg{a.pages + b.pages, a.seconds + b.seconds, a.sessions + b.sessions}
	}
	var rows []Row
	for _, d := range docs {
		if d.MergedInto.Valid {
			continue
		}
		a := sum[d.ID]
		rows = append(rows, Row{Document: d, Pages: a.pages, Minutes: int64(math.RoundToEven(float64(a.seconds) / 60)), Sessions: a.sessions})
	}
	return rows, nil
}
