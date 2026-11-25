// Package goodreads imports a Goodreads library export (CSV) as books. It
// ports src/integrations/imports/goodreads.py. Goodreads has no API, so the
// CSV is the only way in.
//
// Each row is matched to a book by ISBN13, then ISBN, then title and author,
// through Hardcover and then Open Library. The shelf gives the status, the
// 1-5 star rating becomes a 0-10 score, a read book gets its page count as
// progress and its Date Read as finish date. The import is additive: existing
// entries are only filled in and moved forward.
package goodreads

import (
	"context"
	"crypto/rand"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/the-agency-labs/encompass/server/internal/catalog"
	"github.com/the-agency-labs/encompass/server/internal/db"
	"github.com/the-agency-labs/encompass/server/internal/integrations/sourcestatus"
	"github.com/the-agency-labs/encompass/server/internal/jobs"
	"github.com/the-agency-labs/encompass/server/internal/library"
)

const (
	// Source is the source string of the importer's changes.
	Source = "goodreads"
	// SourceName is the importer's name in the Sources status.
	SourceName = "goodreads"
	// MaxUpload is the largest CSV accepted.
	MaxUpload = 10 << 20
	// JobKind is the one-off job that runs an upload.
	JobKind = "goodreads.import"

	minRating  = 1
	maxRating  = 5
	scoreScale = 2
)

// ErrNotUTF8 means the upload is not a text CSV.
var ErrNotUTF8 = errors.New("invalid file format: please upload a CSV file")

// Importer imports Goodreads CSV exports.
type Importer struct {
	Q       *db.Queries
	Library *library.Service
	Catalog *catalog.Catalog
	Now     func() time.Time
	Loc     *time.Location
}

// New builds an importer.
func New(cat *catalog.Catalog, lib *library.Service, q *db.Queries) *Importer {
	return &Importer{Q: q, Library: lib, Catalog: cat}
}

func (im *Importer) now() time.Time {
	if im.Now != nil {
		return im.Now().UTC()
	}
	return time.Now().UTC()
}

func (im *Importer) loc() *time.Location {
	if im.Loc != nil {
		return im.Loc
	}
	if im.Library != nil && im.Library.Loc != nil {
		return im.Library.Loc
	}
	return time.UTC
}

// Result is what an import did.
type Result struct {
	Counts   map[string]int `json:"counts"`
	Warnings []string       `json:"warnings"`
}

// shelfStatus maps an exclusive shelf to a status; "" for shelves we ignore.
func shelfStatus(shelf string) string {
	switch strings.TrimSpace(shelf) {
	case "read":
		return library.Completed
	case "currently-reading":
		return library.InProgress
	case "to-read":
		return library.Planning
	case "did-not-finish":
		return library.Dropped
	}
	return ""
}

// parseRating turns a 1-5 rating ("5" or "5.0", half stars allowed) into a
// 0-10 score; empty, 0, out-of-range or malformed values give no score.
func parseRating(raw string) *float64 {
	v, err := strconv.ParseFloat(strings.TrimSpace(raw), 64)
	if err != nil || v < minRating || v > maxRating {
		return nil
	}
	s := math.Round(v*scoreScale*10) / 10
	return &s
}

// cleanISBN strips the ="..." wrapper Goodreads puts around ISBNs and anything
// that isn't a digit or X.
func cleanISBN(raw string) string {
	var b strings.Builder
	for _, r := range raw {
		if (r >= '0' && r <= '9') || r == 'X' || r == 'x' {
			b.WriteRune(r)
		}
	}
	return strings.ToUpper(b.String())
}

// parseDate reads a Goodreads YYYY/MM/DD date at midnight in loc.
func parseDate(s string, loc *time.Location) (time.Time, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, false
	}
	t, err := time.ParseInLocation("2006/01/02", s, loc)
	if err != nil {
		return time.Time{}, false
	}
	return t.UTC(), true
}

type row map[string]string

func (r row) description() string {
	title, id := r["Title"], r["Book Id"]
	switch {
	case title != "" && id != "":
		return fmt.Sprintf("%s (Goodreads ID %s)", title, id)
	case title != "":
		return title
	case id != "":
		return "Goodreads ID " + id
	}
	return "row"
}

func readRows(data []byte) ([]row, error) {
	if !utf8.Valid(data) {
		return nil, ErrNotUTF8
	}
	text := strings.TrimPrefix(string(data), "\xef\xbb\xbf")
	cr := csv.NewReader(strings.NewReader(text))
	cr.LazyQuotes = true
	cr.FieldsPerRecord = -1
	header, err := cr.Read()
	if errors.Is(err, io.EOF) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("%w (%v)", ErrNotUTF8, err)
	}
	for i := range header {
		header[i] = strings.TrimSpace(header[i])
	}
	var rows []row
	for {
		rec, err := cr.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("%w (%v)", ErrNotUTF8, err)
		}
		r := row{}
		for i, v := range rec {
			if i < len(header) {
				r[header[i]] = v
			}
		}
		rows = append(rows, r)
	}
	return rows, nil
}

type book struct{ source, id, title, image string }

// find matches a row to a book: ISBN13, ISBN, then title and author, in
// Hardcover and then Open Library.
func (im *Importer) find(ctx context.Context, r row) (*book, error) {
	var queries []string
	if v := cleanISBN(r["ISBN13"]); v != "" {
		queries = append(queries, v)
	}
	if v := cleanISBN(r["ISBN"]); v != "" {
		queries = append(queries, v)
	}
	title, author := strings.TrimSpace(r["Title"]), strings.TrimSpace(r["Author"])
	if title != "" {
		if author != "" {
			queries = append(queries, title+" "+author)
		} else {
			queries = append(queries, title)
		}
	}
	var lastErr error
	for _, source := range []string{catalog.SourceHardcover, catalog.SourceOpenLibrary} {
		for _, q := range queries {
			query := q
			if source == catalog.SourceOpenLibrary && cleanISBN(q) == q {
				query = "isbn:" + q
			}
			page, err := im.Catalog.Reg.Search(ctx, catalog.KindBook, source, query, 1, true)
			if err != nil {
				if errors.Is(err, catalog.ErrUnsupported) {
					break // that provider isn't set up; try the next
				}
				lastErr = err
				continue
			}
			if b := firstResult(page, source); b != nil {
				return b, nil
			}
		}
	}
	return nil, lastErr
}

func firstResult(page catalog.SearchPage, source string) *book {
	var results []map[string]any
	switch v := page["results"].(type) {
	case []any:
		for _, e := range v {
			if m, ok := e.(map[string]any); ok {
				results = append(results, m)
			}
		}
	case []map[string]any:
		results = v
	}
	if len(results) == 0 {
		return nil
	}
	m := results[0]
	id := fmt.Sprint(m["media_id"])
	if id == "" || id == "<nil>" {
		return nil
	}
	b := &book{source: source, id: id}
	b.title, _ = m["title"].(string)
	b.image, _ = m["image"].(string)
	return b
}

// item returns the catalog item of a book, from its metadata when the
// provider answers and from the search hit otherwise.
func (im *Importer) item(ctx context.Context, b *book, pages int) (catalog.Item, error) {
	item, err := im.Catalog.EnsureItem(ctx, catalog.KindBook, b.source, b.id)
	if err == nil {
		return item, nil
	}
	if existing, gerr := im.Q.GetItemByKey(ctx, db.GetItemByKeyParams{Source: b.source, ExternalID: b.id, Kind: catalog.KindBook}); gerr == nil {
		return existing, nil
	}
	if b.title == "" {
		return catalog.Item{}, err
	}
	slog.WarnContext(ctx, "goodreads: metadata unavailable, using the search hit", "source", b.source, "id", b.id, "err", err)
	params := db.InsertItemParams{Kind: catalog.KindBook, Source: b.source, ExternalID: b.id, Title: b.title, Image: b.image, Meta: "{}"}
	if pages > 0 {
		params.Total.Int64, params.Total.Valid = int64(pages), true
	}
	return im.Q.InsertItem(ctx, params)
}

// Import imports a CSV for the user.
func (im *Importer) Import(ctx context.Context, userID int64, data []byte) (*Result, error) {
	rows, err := readRows(data)
	if err != nil {
		return nil, err
	}
	res := &Result{Counts: map[string]int{}, Warnings: []string{}}
	loc := im.loc()
	for _, r := range rows {
		if ctx.Err() != nil {
			return res, ctx.Err()
		}
		status := shelfStatus(r["Exclusive Shelf"])
		if status == "" {
			// Skip before hitting the providers; custom shelves aren't imported.
			res.Counts["skipped"]++
			res.Warnings = append(res.Warnings, fmt.Sprintf("%s: Unsupported shelf '%s'.", r.description(), r["Exclusive Shelf"]))
			continue
		}
		if err := im.importRow(ctx, userID, r, status, loc, res); err != nil {
			slog.WarnContext(ctx, "goodreads: row failed", "row", r.description(), "err", err)
			res.Warnings = append(res.Warnings, fmt.Sprintf("Error processing entry: %s - %v", r.description(), err))
		}
	}
	return res, nil
}

func (im *Importer) importRow(ctx context.Context, userID int64, r row, status string, loc *time.Location, res *Result) error {
	b, err := im.find(ctx, r)
	if b == nil {
		if err != nil {
			return err
		}
		res.Counts["unmatched"]++
		res.Warnings = append(res.Warnings, fmt.Sprintf("%s: Couldn't find this book via ISBN13, ISBN or Title in Hardcover or Open Library", r["Title"]))
		return nil
	}
	pages := 0
	if n, err := strconv.Atoi(strings.TrimSpace(r["Number of Pages"])); err == nil && n > 0 {
		pages = n
	}
	item, err := im.item(ctx, b, pages)
	if err != nil {
		return err
	}
	v := library.SyncValues{Status: status, Score: parseRating(r["My Rating"]), Notes: r["Private Notes"]}
	if status == library.Completed {
		v.Progress = pages
	}
	read, hasRead := parseDate(r["Date Read"], loc)
	added, hasAdded := parseDate(r["Date Added"], loc)
	if hasRead {
		v.FinishedAt = &read
	}
	// The newest of the two dates dates the change (activity), like the
	// history date of the Python importer.
	at := im.now()
	switch {
	case hasRead && hasAdded:
		at = read
		if added.After(read) {
			at = added
		}
	case hasRead:
		at = read
	case hasAdded:
		at = added
	}
	_, curErr := im.Library.CurrentEntry(ctx, userID, item.ID)
	changed, err := im.Library.SyncEntry(ctx, userID, item.ID, v, Source, at)
	if err != nil {
		return err
	}
	switch {
	case curErr != nil: // no entry before: a new book
		res.Counts["books"]++
	case changed:
		res.Counts["updated"]++
	}
	return nil
}

// Stash stores an upload for a job to read (uploads are too big for the job
// payload) and returns its key. The data is validated as text first.
func (im *Importer) Stash(ctx context.Context, userID int64, data []byte) (string, error) {
	if !utf8.Valid(data) {
		return "", ErrNotUTF8
	}
	var buf [8]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", err
	}
	key := fmt.Sprintf("upload:goodreads:%d:%s", userID, hex.EncodeToString(buf[:]))
	err := im.Q.SetKV(ctx, db.SetKVParams{Key: key, Value: string(data), UpdatedAt: im.now().Format(time.RFC3339)})
	return key, err
}

// RunUpload imports a stashed upload, clears it and records the Sources status.
func (im *Importer) RunUpload(ctx context.Context, userID int64, key string) (*Result, error) {
	data, err := im.Q.GetKV(ctx, key)
	if err != nil {
		return nil, fmt.Errorf("upload is gone: %w", err)
	}
	res, err := im.Import(ctx, userID, []byte(data))
	at := im.now()
	status := sourcestatus.New(SourceName, at, nil, nil, "", false)
	if err != nil {
		status.Message = err.Error()
	} else {
		status = sourcestatus.New(SourceName, at, res.Counts, res.Warnings, "", len(res.Warnings) == 0)
		if len(res.Warnings) > 0 {
			status.Message = fmt.Sprintf("%d rows had problems", len(res.Warnings))
		}
	}
	wctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if werr := sourcestatus.Write(wctx, im.Q, userID, status); werr != nil {
		slog.ErrorContext(ctx, "goodreads: write source status", "err", werr)
	}
	// The file served its purpose; don't keep up to 10 MB around.
	_ = im.Q.SetKV(wctx, db.SetKVParams{Key: key, Value: "", UpdatedAt: at.Format(time.RFC3339)})
	return res, err
}

// RegisterJobs handles the "goodreads.import" one-off job ({"key": ...}).
func (im *Importer) RegisterJobs(m *jobs.Manager) {
	m.Handle(JobKind, func(ctx context.Context, job jobs.Job) (string, error) {
		var p struct {
			Key string `json:"key"`
		}
		if err := json.Unmarshal(job.Payload, &p); err != nil || p.Key == "" {
			return "", errors.New("goodreads: job has no upload")
		}
		res, err := im.RunUpload(ctx, job.UserID, p.Key)
		if err != nil {
			if errors.Is(err, ErrNotUTF8) {
				return "", nil // retrying can't fix a bad file; the status says so
			}
			return "", err
		}
		return fmt.Sprintf("%v", res.Counts), nil
	})
}
