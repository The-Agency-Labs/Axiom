package httpapi

import (
	"database/sql"
	"errors"
	"log/slog"
	"net/http"
	"sort"
	"strings"

	"github.com/the-agency-labs/encompass/server/internal/auth"
	"github.com/the-agency-labs/encompass/server/internal/catalog"
	"github.com/the-agency-labs/encompass/server/internal/db"
)

// seriesRoutes registers the book series endpoints:
//
//	GET /series                          the viewer's series with progress
//	GET /series/search?q=                search series by name (local, then Hardcover)
//	GET /series/{id}                     a stored series with the viewer's entries
//	GET /series/{source}/{external_id}   fetch a remote series on demand, same shape
func (s *Server) seriesRoutes(mux *http.ServeMux) {
	mux.Handle("GET /api/v1/series", auth.Require(JSON(func(w http.ResponseWriter, r *http.Request) (any, error) {
		results, err := s.userSeries(r.Context(), auth.User(r.Context()).ID)
		if err != nil {
			return nil, err
		}
		return map[string]any{"results": results}, nil
	})))

	mux.Handle("GET /api/v1/series/search", auth.Require(JSON(func(w http.ResponseWriter, r *http.Request) (any, error) {
		return s.seriesSearch(r)
	})))

	mux.Handle("GET /api/v1/series/{id}", auth.Require(JSON(func(w http.ResponseWriter, r *http.Request) (any, error) {
		id, err := libraryPathInt(r, "id")
		if err != nil {
			return nil, err
		}
		ser, err := s.Queries.GetSeries(r.Context(), id)
		if err != nil {
			return nil, seriesError(err)
		}
		return s.seriesDetailJSON(r.Context(), auth.User(r.Context()).ID, ser)
	})))

	mux.Handle("GET /api/v1/series/{source}/{external_id}", auth.Require(JSON(func(w http.ResponseWriter, r *http.Request) (any, error) {
		ser, err := s.Catalog.FetchSeries(r.Context(), r.PathValue("source"), r.PathValue("external_id"))
		if err != nil {
			return nil, seriesError(err)
		}
		return s.seriesDetailJSON(r.Context(), auth.User(r.Context()).ID, ser)
	})))
}

// seriesSearch finds series by name: stored ones first (normalised name
// contains the normalised query), then Hardcover's, skipping repeats. A
// Hardcover failure leaves the local results.
func (s *Server) seriesSearch(r *http.Request) (any, error) {
	ctx := r.Context()
	userID := auth.User(ctx).ID
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	results := []map[string]any{}
	if q == "" {
		return map[string]any{"results": results}, nil
	}
	seen := map[[2]string]bool{}
	key := catalog.SeriesKey(q)
	if key != "" {
		rows, err := s.Queries.SearchSeriesByName(ctx, db.SearchSeriesByNameParams{
			UserID: userID, Pattern: sql.NullString{String: key, Valid: true},
		})
		if err != nil {
			return nil, err
		}
		sort.SliceStable(rows, func(i, j int) bool {
			return catalog.SeriesKey(rows[i].Name) == key && catalog.SeriesKey(rows[j].Name) != key
		})
		for _, row := range rows {
			seen[[2]string{row.Source, row.ExternalID}] = true
			results = append(results, map[string]any{
				"id": row.ID, "source": row.Source, "external_id": row.ExternalID, "name": row.Name,
				"books_count": row.BooksCount, "tracked": row.Tracked,
			})
		}
	}
	hits, err := s.Catalog.SearchRemoteSeries(ctx, q)
	if err != nil {
		slog.WarnContext(ctx, "series search: remote failed", "err", err)
	}
	for _, h := range hits {
		if seen[[2]string{catalog.SourceHardcover, h.ExternalID}] {
			continue
		}
		seen[[2]string{catalog.SourceHardcover, h.ExternalID}] = true
		res := map[string]any{
			"id": nil, "source": catalog.SourceHardcover, "external_id": h.ExternalID,
			"name": catalog.NormalizeSeriesName(h.Name), "books_count": h.BooksCount, "tracked": 0,
		}
		// Already stored under another name: point at the local copy.
		local, err := s.Queries.GetSeriesByKey(ctx, db.GetSeriesByKeyParams{Source: catalog.SourceHardcover, ExternalID: h.ExternalID})
		switch {
		case err == nil:
			n, _ := s.Queries.CountTrackedInSeries(ctx, db.CountTrackedInSeriesParams{UserID: userID, SeriesID: local.ID})
			res["id"], res["tracked"] = local.ID, n
		case !errors.Is(err, sql.ErrNoRows):
			return nil, err
		}
		results = append(results, res)
	}
	return map[string]any{"results": results}, nil
}
