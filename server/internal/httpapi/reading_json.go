package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/the-agency-labs/encompass/server/internal/catalog"
	"github.com/the-agency-labs/encompass/server/internal/library"
	"github.com/the-agency-labs/encompass/server/internal/reading"
)

// readingGo runs background work started by a request (auto-linking after an
// upload, so provider calls don't hold up the KOReader plugin's 8 s timeout).
// Tests replace it with a synchronous runner.
var readingGo = func(f func()) { go f() }

func (s *Server) readingService() *reading.Service {
	return reading.New(s.DB, s.Queries, s.Catalog, s.Library)
}

// readingISO formats a stored timestamp like Python's datetime.isoformat().
func readingISO(stamp string) string {
	t, err := time.Parse(time.RFC3339, stamp)
	if err != nil {
		return stamp
	}
	return t.UTC().Format("2006-01-02T15:04:05-07:00")
}

func readingNullISO(v sql.NullString) any {
	if !v.Valid {
		return nil
	}
	return readingISO(v.String)
}

// readingErr maps domain errors to the API's HTTP errors.
func readingErr(err error) error {
	var apiErr *Error
	switch {
	case err == nil, errors.As(err, &apiErr):
		return err
	case errors.Is(err, reading.ErrNotFound), errors.Is(err, library.ErrNoEntry), errors.Is(err, catalog.ErrNotFound):
		return Errorf(http.StatusNotFound, "Not Found")
	case errors.Is(err, reading.ErrInvalid):
		return Errorf(http.StatusBadRequest, strings.TrimPrefix(err.Error(), reading.ErrInvalid.Error()+": "))
	case errors.Is(err, reading.ErrUnsupported):
		return Errorf(http.StatusBadRequest, "Unsupported source")
	}
	return err
}

// readingItemCache loads items once per request.
type readingItemCache struct {
	cat   *catalog.Catalog
	mu    sync.Mutex
	items map[int64]any
	// series is the stored primary series of each linked book (nil: none).
	series map[int64]*catalog.ItemSeries
}

func newReadingItemCache(cat *catalog.Catalog) *readingItemCache {
	return &readingItemCache{cat: cat, items: map[int64]any{}, series: map[int64]*catalog.ItemSeries{}}
}

// dict returns the item_dict of src/api/serializers.py, or nil.
func (c *readingItemCache) dict(ctx context.Context, id sql.NullInt64) any {
	if !id.Valid {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if v, ok := c.items[id.Int64]; ok {
		return v
	}
	var out any
	if it, err := c.cat.GetItem(ctx, id.Int64); err == nil {
		c.series[id.Int64] = catalog.ItemSeriesFromMeta(it.Meta)
		out = map[string]any{
			"id": it.ID, "media_id": it.ExternalID, "source": it.Source, "media_type": it.Kind,
			"title": it.Title, "image": it.Image, "season_number": nil, "episode_number": nil,
		}
	}
	c.items[id.Int64] = out
	return out
}

func readingNullInt(v sql.NullInt64) any {
	if !v.Valid {
		return nil
	}
	return v.Int64
}

// documentDict is document_dict plus the linking state ("ignored", "merged_into").
func (c *readingItemCache) documentDict(ctx context.Context, d reading.Document) map[string]any {
	var pct any
	if d.Percentage.Valid {
		pct = d.Percentage.Float64
	}
	// display_title: the linked book's title, else "Series N: Title" read
	// from the Kindle title (author and edition noise dropped).
	disp := reading.DisplayName(d.Title, d.Authors)
	item := c.dict(ctx, d.ItemID)
	// A linked book prefers the series stored with it over the Kindle title's.
	if d.ItemID.Valid {
		c.mu.Lock()
		stored := c.series[d.ItemID.Int64]
		c.mu.Unlock()
		if stored != nil {
			sameSeries := catalog.SeriesKey(disp.Series) == catalog.SeriesKey(stored.Name)
			if stored.Position > 0 || !sameSeries {
				disp.SeriesNumber = stored.Position
			}
			disp.Series = stored.Name
		}
	}
	display := disp.String()
	if m, ok := item.(map[string]any); ok {
		if t, ok := m["title"].(string); ok && t != "" {
			display = t
			if disp.Series != "" && !strings.Contains(strings.ToLower(t), strings.ToLower(disp.Series)) {
				display = (reading.Display{Title: t, Series: disp.Series, SeriesNumber: disp.SeriesNumber}).String()
			}
		}
	}
	var seriesNum any
	if disp.SeriesNumber > 0 {
		seriesNum = disp.SeriesNumber
	}
	var series any
	if disp.Series != "" {
		series = disp.Series
	}
	return map[string]any{
		"display_title": display, "series": series, "series_number": seriesNum,
		"id": d.ID, "document_hash": d.DocumentHash, "title": d.Title, "authors": d.Authors,
		"total_pages": readingNullInt(d.TotalPages), "percentage": pct, "device": d.Device,
		"progress_updated_at": readingNullISO(d.ProgressUpdatedAt),
		"item":                item,
		"ignored":             d.Ignored != 0, "merged_into": readingNullInt(d.MergedInto),
	}
}

func (c *readingItemCache) sessionDict(ctx context.Context, r reading.SessionRow) map[string]any {
	ss := r.Session
	return map[string]any{
		"id": ss.ID, "source": ss.Source, "title": ss.Title, "item": c.dict(ctx, ss.ItemID),
		"document_id": readingNullInt(r.DocumentID), "started_at": readingISO(ss.StartedAt),
		"ended_at": readingISO(ss.EndedAt), "minutes": readingRoundHalfEven(float64(ss.DurationSeconds) / 60),
		"pages_read": ss.PagesRead, "chapters_read": ss.ChaptersRead,
		"start_page": readingNullInt(ss.StartPage), "end_page": readingNullInt(ss.EndPage), "device": ss.Device,
	}
}

func readingRoundHalfEven(v float64) int64 {
	r := int64(v)
	diff := v - float64(r)
	if diff > 0.5 || (diff == 0.5 && r%2 == 1) {
		r++
	}
	return r
}

// readingBool parses a query boolean the way django-ninja does.
func readingBool(r *http.Request, name string, def bool) (bool, error) {
	v := r.URL.Query().Get(name)
	if v == "" {
		return def, nil
	}
	switch strings.ToLower(v) {
	case "1", "true", "t", "yes", "y", "on":
		return true, nil
	case "0", "false", "f", "no", "n", "off":
		return false, nil
	}
	return def, Errorf(http.StatusUnprocessableEntity, name+" must be a boolean")
}

func readingPathID(r *http.Request, name string) (int64, error) {
	id, err := strconv.ParseInt(r.PathValue(name), 10, 64)
	if err != nil {
		return 0, Errorf(http.StatusUnprocessableEntity, name+" must be an integer")
	}
	return id, nil
}

// readingDecodeOptional decodes a JSON body that may be empty.
func readingDecodeOptional(r *http.Request, dst any) error {
	r.Body = http.MaxBytesReader(nil, r.Body, 1<<20)
	if err := json.NewDecoder(r.Body).Decode(dst); err != nil && !errors.Is(err, io.EOF) {
		return Errorf(http.StatusUnprocessableEntity, "Invalid JSON body: "+err.Error())
	}
	return nil
}

// readingFlexString accepts a JSON string or number.
type readingFlexString string

func (f *readingFlexString) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err == nil {
		*f = readingFlexString(s)
		return nil
	}
	var n json.Number
	if err := json.Unmarshal(b, &n); err != nil {
		return errors.New("must be a string")
	}
	*f = readingFlexString(n.String())
	return nil
}
