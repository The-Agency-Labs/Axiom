package reading

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"sort"
	"time"

	"github.com/the-agency-labs/encompass/server/internal/db"
)

// JournalBook is one book's reading on one day.
type JournalBook struct {
	ItemID       *int64   `json:"item_id"`
	DocumentID   *int64   `json:"document_id"`
	Title        string   `json:"title"`
	Series       *string  `json:"series"`
	SeriesNumber *float64 `json:"series_number"`
	Image        string   `json:"image"`
	Minutes      int64    `json:"minutes"`
	Pages        int64    `json:"pages"`
	Chapters     int64    `json:"chapters"`
	FromPage     *int64   `json:"from_page"`
	ToPage       *int64   `json:"to_page"`
	seconds      int64
}

// JournalDay is everything read on one local day, merged per book.
type JournalDay struct {
	Date     string        `json:"date"`
	Minutes  int64         `json:"minutes"`
	Pages    int64         `json:"pages"`
	Chapters int64         `json:"chapters"`
	Books    []JournalBook `json:"books"`
}

// Journal returns the reading log for local days start..end inclusive,
// newest day first: sessions merged per day (in the service's zone) and per
// book. A book is its linked item, else its document (merged duplicates
// roll up to the document they were merged into), else the session title.
// Days without reading are left out; within a day books are ordered by time
// spent.
func (s *Service) Journal(ctx context.Context, userID int64, start, end time.Time) ([]JournalDay, error) {
	from, to := bounds(start, end)
	rows, err := s.Q.ListReadingSessions(ctx, db.ListReadingSessionsParams{UserID: userID, FromTs: from, ToTs: to, MaxRows: 1_000_000})
	if err != nil {
		return nil, err
	}
	type key struct {
		day  string
		kind byte
		id   int64
		name string
	}
	books := map[key]*JournalBook{}
	docs := map[int64]Document{}
	items := map[int64]struct{ title, image string }{}
	for _, r := range rows {
		ss := r.ReadingSession
		day := s.localDay(ss.StartedAt).Format("2006-01-02")
		var docID int64
		if r.EffectiveDocumentID > 0 {
			docID = r.EffectiveDocumentID
		}
		itemID := ss.ItemID
		var doc Document
		if docID > 0 {
			d, ok := docs[docID]
			if !ok {
				if d, err = s.Q.GetReadingDocument(ctx, db.GetReadingDocumentParams{ID: docID, UserID: userID}); err != nil && !errors.Is(err, sql.ErrNoRows) {
					return nil, err
				}
				docs[docID] = d
			}
			doc = d
			if !itemID.Valid && doc.ItemID.Valid {
				itemID = doc.ItemID
			}
		}
		k := key{day: day, kind: 't', name: ss.Title}
		switch {
		case itemID.Valid:
			k = key{day: day, kind: 'i', id: itemID.Int64}
		case docID > 0:
			k = key{day: day, kind: 'd', id: docID}
		}
		b := books[k]
		if b == nil {
			b = &JournalBook{Title: ss.Title}
			if docID > 0 {
				id := docID
				b.DocumentID = &id
				disp := DisplayName(doc.Title, doc.Authors)
				b.Title = disp.String()
				if disp.Series != "" {
					sr, n := disp.Series, disp.SeriesNumber
					b.Series = &sr
					if n > 0 {
						b.SeriesNumber = &n
					}
				}
			}
			if itemID.Valid {
				id := itemID.Int64
				b.ItemID = &id
				it, ok := items[id]
				if !ok && s.Catalog != nil {
					if item, err := s.Catalog.GetItem(ctx, id); err == nil {
						it = struct{ title, image string }{item.Title, item.Image}
					}
					items[id] = it
				}
				if it.title != "" {
					b.Title, b.Image = it.title, it.image
				}
			}
			books[k] = b
		}
		b.seconds += ss.DurationSeconds
		b.Pages += ss.PagesRead
		b.Chapters += ss.ChaptersRead
		if ss.StartPage.Valid && (b.FromPage == nil || ss.StartPage.Int64 < *b.FromPage) {
			v := ss.StartPage.Int64
			b.FromPage = &v
		}
		if ss.EndPage.Valid && (b.ToPage == nil || ss.EndPage.Int64 > *b.ToPage) {
			v := ss.EndPage.Int64
			b.ToPage = &v
		}
	}

	byDay := map[string]*JournalDay{}
	for k, b := range books {
		b.Minutes = int64(math.RoundToEven(float64(b.seconds) / 60))
		d := byDay[k.day]
		if d == nil {
			d = &JournalDay{Date: k.day}
			byDay[k.day] = d
		}
		d.Books = append(d.Books, *b)
	}
	out := make([]JournalDay, 0, len(byDay))
	for _, d := range byDay {
		var secs int64
		for _, b := range d.Books {
			secs += b.seconds
			d.Pages += b.Pages
			d.Chapters += b.Chapters
		}
		d.Minutes = int64(math.RoundToEven(float64(secs) / 60))
		sort.Slice(d.Books, func(i, j int) bool {
			if d.Books[i].seconds != d.Books[j].seconds {
				return d.Books[i].seconds > d.Books[j].seconds
			}
			return d.Books[i].Title < d.Books[j].Title
		})
		out = append(out, *d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Date > out[j].Date })
	return out, nil
}
