package httpapi

import (
	"context"
	"database/sql"
	"errors"
	"net/http"

	"github.com/the-agency-labs/axiom/server/internal/catalog"
	"github.com/the-agency-labs/axiom/server/internal/db"
	"github.com/the-agency-labs/axiom/server/internal/providers"
)

// seriesRefJSON is the {"id","name","position"} of a book's primary series
// read from items.meta, nil when it has none.
func seriesRefJSON(meta string) any {
	s := catalog.ItemSeriesFromMeta(meta)
	if s == nil {
		return nil
	}
	return map[string]any{"id": s.ID, "name": s.Name, "position": s.Position}
}

// seriesError maps catalog and provider errors of the series endpoints.
func seriesError(err error) error {
	var apiErr *Error
	var provErr *providers.Error
	switch {
	case err == nil, errors.As(err, &apiErr):
		return err
	case errors.Is(err, catalog.ErrNotFound), errors.Is(err, sql.ErrNoRows):
		return Errorf(http.StatusNotFound, "Not Found")
	case errors.Is(err, catalog.ErrUnsupported):
		return Errorf(http.StatusBadRequest, err.Error())
	case errors.As(err, &provErr):
		return Errorf(http.StatusBadGateway, "Series metadata unavailable")
	}
	return err
}

func seriesEntryJSON(id sql.NullInt64, status sql.NullString, progress sql.NullInt64, score sql.NullFloat64) any {
	if !id.Valid {
		return nil
	}
	return map[string]any{
		"id": id.Int64, "status": status.String, "progress": progress.Int64, "score": libraryNullFloat(score),
	}
}

func seriesYear(y sql.NullInt64) any {
	if !y.Valid {
		return nil
	}
	return y.Int64
}

func seriesItemID(id sql.NullInt64) any {
	if !id.Valid {
		return nil
	}
	return id.Int64
}

// seriesDetailJSON is the series page: every book in position order with
// the viewer's own entry (null when untracked).
func (s *Server) seriesDetailJSON(ctx context.Context, userID int64, ser db.Series) (map[string]any, error) {
	rows, err := s.Queries.ListSeriesBooksForUser(ctx, db.ListSeriesBooksForUserParams{UserID: userID, SeriesID: ser.ID})
	if err != nil {
		return nil, err
	}
	books := make([]map[string]any, 0, len(rows))
	for _, r := range rows {
		books = append(books, map[string]any{
			"position": r.Position, "title": r.Title, "image": r.Image, "year": seriesYear(r.Year),
			"media_type": catalog.KindBook, "source": r.Source, "media_id": r.ExternalID,
			"item_id": seriesItemID(r.ItemID),
			"entry":   seriesEntryJSON(r.EntryID, r.EntryStatus, r.EntryProgress, r.EntryScore),
		})
	}
	count := int(ser.BooksCount)
	if count < len(books) {
		count = len(books)
	}
	return map[string]any{"id": ser.ID, "name": ser.Name, "books_count": count, "books": books}, nil
}

// seriesSummary is one row of GET /series.
type seriesSummary struct {
	ID        int64
	Name      string
	Total     int
	Tracked   int
	Completed int
	Image     string
	Next      map[string]any
}

// userSeries aggregates the viewer's series: counts and the next unread book
// (lowest position whose entry is not Completed, tracked or not).
func (s *Server) userSeries(ctx context.Context, userID int64) ([]map[string]any, error) {
	rows, err := s.Queries.ListUserSeriesBooks(ctx, userID)
	if err != nil {
		return nil, err
	}
	var order []int64
	by := map[int64]*seriesSummary{}
	for _, r := range rows {
		sm := by[r.SeriesID]
		if sm == nil {
			sm = &seriesSummary{ID: r.SeriesID, Name: r.Name}
			by[r.SeriesID] = sm
			order = append(order, r.SeriesID)
		}
		sm.Total++
		if sm.Image == "" {
			sm.Image = r.Image
		}
		if r.EntryID.Valid {
			sm.Tracked++
		}
		done := r.EntryStatus.String == "Completed"
		if done {
			sm.Completed++
		}
		if !done && sm.Next == nil {
			var status any
			if r.EntryStatus.Valid {
				status = r.EntryStatus.String
			}
			sm.Next = map[string]any{
				"position": r.Position, "title": r.Title, "image": r.Image, "media_type": catalog.KindBook,
				"source": r.Source, "media_id": r.ExternalID, "item_id": seriesItemID(r.ItemID), "status": status,
			}
		}
	}
	out := make([]map[string]any, 0, len(order))
	for _, id := range order {
		sm := by[id]
		var next any
		if sm.Next != nil {
			next = sm.Next
		}
		out = append(out, map[string]any{
			"id": sm.ID, "name": sm.Name, "image": sm.Image,
			"tracked": sm.Tracked, "completed": sm.Completed, "total": sm.Total, "next": next,
		})
	}
	return out, nil
}
