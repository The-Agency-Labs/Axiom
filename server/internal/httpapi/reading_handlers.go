package httpapi

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/the-agency-labs/encompass/server/internal/auth"
	"github.com/the-agency-labs/encompass/server/internal/reading"
)

// readingDateRange parses ?start= and ?end= (YYYY-MM-DD) with the defaults of
// src/api/reading.py.
func (s *Server) readingDateRange(r *http.Request, svc *reading.Service) (time.Time, time.Time, error) {
	var start, end *time.Time
	for name, dst := range map[string]**time.Time{"start": &start, "end": &end} {
		v := r.URL.Query().Get(name)
		if v == "" {
			continue
		}
		t, err := svc.ParseDate(v)
		if err != nil {
			return t, t, Errorf(http.StatusUnprocessableEntity, name+" must be a date (YYYY-MM-DD)")
		}
		*dst = &t
	}
	return svc.DateRange(start, end)
}

type readingPageStatIn struct {
	Page       *int64 `json:"page"`
	StartTime  *int64 `json:"start_time"`
	Duration   *int64 `json:"duration"`
	TotalPages *int64 `json:"total_pages"`
}

type readingBookIn struct {
	MD5        *string             `json:"md5"`
	Title      string              `json:"title"`
	Authors    string              `json:"authors"`
	TotalPages *int64              `json:"total_pages"`
	PageStats  []readingPageStatIn `json:"page_stats"`
}

// readingUploadKOReader ingests the KOReader plugin's statistics upload.
func (s *Server) readingUploadKOReader(w http.ResponseWriter, r *http.Request) (any, error) {
	var in struct {
		Device string           `json:"device"`
		Books  *[]readingBookIn `json:"books"`
	}
	if err := DecodeJSON(r, &in); err != nil {
		return nil, err
	}
	if in.Books == nil {
		return nil, Errorf(http.StatusUnprocessableEntity, "books is required")
	}
	books := make([]reading.KOBook, 0, len(*in.Books))
	for _, b := range *in.Books {
		if b.MD5 == nil || *b.MD5 == "" {
			return nil, Errorf(http.StatusUnprocessableEntity, "md5 is required")
		}
		kb := reading.KOBook{MD5: *b.MD5, Title: b.Title, Authors: b.Authors, TotalPages: b.TotalPages}
		for _, st := range b.PageStats {
			if st.Page == nil || st.StartTime == nil || st.Duration == nil {
				return nil, Errorf(http.StatusUnprocessableEntity, "page, start_time and duration are required")
			}
			kb.PageStats = append(kb.PageStats, reading.PageStat{Page: *st.Page, StartTime: *st.StartTime, Duration: *st.Duration, TotalPages: st.TotalPages})
		}
		books = append(books, kb)
	}

	svc := s.readingService()
	userID := auth.User(r.Context()).ID
	results := []map[string]any{}
	for _, kb := range books {
		res, err := svc.IngestKOReader(r.Context(), userID, kb, in.Device)
		if err != nil {
			return nil, err
		}
		doc := res.Document
		if !doc.ItemID.Valid && !doc.MergedInto.Valid && doc.Ignored == 0 && !doc.LinkCheckedAt.Valid {
			readingGo(func() { svc.AutoLinkInBackground(doc) })
		}
		results = append(results, map[string]any{
			"document_id": doc.ID, "md5": doc.DocumentHash, "new_page_events": res.Added,
			"linked": doc.ItemID.Valid || doc.MergedInto.Valid,
		})
	}
	return map[string]any{"books": results}, nil
}

func (s *Server) readingStats(w http.ResponseWriter, r *http.Request) (any, error) {
	svc := s.readingService()
	start, end, err := s.readingDateRange(r, svc)
	if err != nil {
		return nil, readingErr(err)
	}
	userID := auth.User(r.Context()).ID
	days, err := svc.DailyStats(r.Context(), userID, start, end)
	if err != nil {
		return nil, err
	}
	current, longest, err := svc.Streaks(r.Context(), userID)
	if err != nil {
		return nil, err
	}
	today := svc.Today().Format("2006-01-02")
	var todayRow *reading.Day
	var pages, chapters, minutes, sessions, daysRead int64
	for i, d := range days {
		if d.Date == today {
			todayRow = &days[i]
		}
		pages += d.Pages
		chapters += d.Chapters
		minutes += d.Minutes
		sessions += d.Sessions
		if d.Pages > 0 || d.Chapters > 0 {
			daysRead++
		}
	}
	return map[string]any{
		"start": start.Format("2006-01-02"), "end": end.Format("2006-01-02"),
		"read_today":     todayRow != nil && (todayRow.Pages > 0 || todayRow.Chapters > 0),
		"today":          todayRow,
		"current_streak": current, "longest_streak": longest,
		"totals": map[string]any{
			"pages": pages, "chapters": chapters, "minutes": minutes, "sessions": sessions,
			"days_read": daysRead, "days_in_range": len(days),
		},
		"days": days,
	}, nil
}

func (s *Server) readingListSessions(w http.ResponseWriter, r *http.Request) (any, error) {
	svc := s.readingService()
	start, end, err := s.readingDateRange(r, svc)
	if err != nil {
		return nil, readingErr(err)
	}
	limit := 100
	if v := r.URL.Query().Get("limit"); v != "" {
		if limit, err = strconv.Atoi(v); err != nil {
			return nil, Errorf(http.StatusUnprocessableEntity, "limit must be an integer")
		}
	}
	rows, err := svc.ListSessions(r.Context(), auth.User(r.Context()).ID, start, end, limit)
	if err != nil {
		return nil, err
	}
	items := newReadingItemCache(s.Catalog)
	out := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		out = append(out, items.sessionDict(r.Context(), row))
	}
	return out, nil
}

// readingParseTime accepts ISO 8601 as pydantic does: with an offset or Z, or
// without (then in the server's zone).
func readingParseTime(v string, loc *time.Location) (time.Time, bool) {
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02T15:04:05.999999999", "2006-01-02 15:04:05.999999999", "2006-01-02"} {
		var t time.Time
		var err error
		if strings.HasSuffix(layout, "Z07:00") {
			t, err = time.Parse(layout, v)
		} else {
			t, err = time.ParseInLocation(layout, v, loc)
		}
		if err == nil {
			return t.UTC(), true
		}
	}
	return time.Time{}, false
}

func (s *Server) readingCreateSession(w http.ResponseWriter, r *http.Request) (any, error) {
	var in struct {
		Source       *string `json:"source"`
		Title        string  `json:"title"`
		ItemID       *int64  `json:"item_id"`
		StartedAt    *string `json:"started_at"`
		EndedAt      *string `json:"ended_at"`
		PagesRead    int64   `json:"pages_read"`
		ChaptersRead int64   `json:"chapters_read"`
		StartPage    *int64  `json:"start_page"`
		EndPage      *int64  `json:"end_page"`
		Device       string  `json:"device"`
	}
	if err := DecodeJSON(r, &in); err != nil {
		return nil, err
	}
	svc := s.readingService()
	if in.StartedAt == nil || in.EndedAt == nil {
		return nil, Errorf(http.StatusUnprocessableEntity, "started_at and ended_at are required")
	}
	started, ok1 := readingParseTime(*in.StartedAt, svc.Loc)
	ended, ok2 := readingParseTime(*in.EndedAt, svc.Loc)
	if !ok1 || !ok2 {
		return nil, Errorf(http.StatusUnprocessableEntity, "started_at and ended_at must be ISO 8601 datetimes")
	}
	if in.PagesRead < 0 || in.ChaptersRead < 0 {
		return nil, Errorf(http.StatusUnprocessableEntity, "pages_read and chapters_read must not be negative")
	}
	source := reading.SourceManual
	if in.Source != nil {
		source = *in.Source
	}
	sess, err := svc.CreateSession(r.Context(), auth.User(r.Context()).ID, reading.NewSession{
		Source: source, Title: in.Title, ItemID: in.ItemID, StartedAt: started, EndedAt: ended,
		PagesRead: in.PagesRead, ChaptersRead: in.ChaptersRead, StartPage: in.StartPage, EndPage: in.EndPage, Device: in.Device,
	})
	if err != nil {
		return nil, readingErr(err)
	}
	items := newReadingItemCache(s.Catalog)
	return items.sessionDict(r.Context(), reading.SessionRow{Session: sess, DocumentID: sess.DocumentID}), nil
}

// readingListDocuments lists the books KOReader has reported with their
// totals; unlinked ones carry their top suggestions.
func (s *Server) readingListDocuments(w http.ResponseWriter, r *http.Request) (any, error) {
	unlinked, err := readingBool(r, "unlinked", false)
	if err != nil {
		return nil, err
	}
	withSuggestions, err := readingBool(r, "suggestions", true)
	if err != nil {
		return nil, err
	}
	svc := s.readingService()
	rows, err := svc.Summary(r.Context(), auth.User(r.Context()).ID)
	if err != nil {
		return nil, err
	}
	items := newReadingItemCache(s.Catalog)
	out := make([]map[string]any, len(rows))
	keep := make([]bool, len(rows))
	var wg sync.WaitGroup
	sem := make(chan struct{}, 4)
	for i, row := range rows {
		d := row.Document
		pending := !d.ItemID.Valid && d.Ignored == 0
		if unlinked && !pending {
			continue
		}
		keep[i] = true
		m := items.documentDict(r.Context(), d)
		m["pages_read"], m["minutes"], m["sessions"] = row.Pages, row.Minutes, row.Sessions
		out[i] = m
		if pending && withSuggestions {
			m["suggestions"] = []any{}
			wg.Add(1)
			go func() {
				defer wg.Done()
				sem <- struct{}{}
				defer func() { <-sem }()
				if sug, err := svc.TopSuggestions(r.Context(), d); err == nil {
					m["suggestions"] = sug
				}
			}()
		}
	}
	wg.Wait()
	res := make([]map[string]any, 0, len(rows))
	for i, m := range out {
		if keep[i] {
			res = append(res, m)
		}
	}
	return res, nil
}

func (s *Server) readingDocument(r *http.Request) (reading.Document, error) {
	id, err := readingPathID(r, "id")
	if err != nil {
		return reading.Document{}, err
	}
	doc, err := s.readingService().Get(r.Context(), auth.User(r.Context()).ID, id)
	return doc, readingErr(err)
}

func (s *Server) readingSuggestions(w http.ResponseWriter, r *http.Request) (any, error) {
	doc, err := s.readingDocument(r)
	if err != nil {
		return nil, err
	}
	res, err := s.readingService().Suggestions(r.Context(), doc, r.URL.Query().Get("source"))
	if err != nil {
		if mapped := readingErr(err); mapped != err {
			return nil, mapped
		}
		return nil, Errorf(http.StatusBadGateway, "Search provider unavailable")
	}
	return res, nil
}

func (s *Server) readingLink(w http.ResponseWriter, r *http.Request) (any, error) {
	doc, err := s.readingDocument(r)
	if err != nil {
		return nil, err
	}
	var in struct {
		Source  *string            `json:"source"`
		MediaID *readingFlexString `json:"media_id"`
	}
	if err := DecodeJSON(r, &in); err != nil {
		return nil, err
	}
	if in.Source == nil || in.MediaID == nil || *in.Source == "" || *in.MediaID == "" {
		return nil, Errorf(http.StatusUnprocessableEntity, "source and media_id are required")
	}
	doc, err = s.readingService().LinkToBook(r.Context(), doc, *in.Source, string(*in.MediaID))
	if err != nil {
		if mapped := readingErr(err); mapped != err {
			return nil, mapped
		}
		return nil, Errorf(http.StatusBadGateway, "Metadata provider unavailable")
	}
	return newReadingItemCache(s.Catalog).documentDict(r.Context(), doc), nil
}

func (s *Server) readingMerge(w http.ResponseWriter, r *http.Request) (any, error) {
	doc, err := s.readingDocument(r)
	if err != nil {
		return nil, err
	}
	var in struct {
		Into *json.Number `json:"into"`
	}
	if err := DecodeJSON(r, &in); err != nil {
		return nil, err
	}
	if in.Into == nil {
		return nil, Errorf(http.StatusUnprocessableEntity, "into is required")
	}
	into, err := in.Into.Int64()
	if err != nil {
		return nil, Errorf(http.StatusUnprocessableEntity, "into must be an integer")
	}
	doc, err = s.readingService().Merge(r.Context(), doc, into)
	if err != nil {
		return nil, readingErr(err)
	}
	return newReadingItemCache(s.Catalog).documentDict(r.Context(), doc), nil
}

func (s *Server) readingIgnore(w http.ResponseWriter, r *http.Request) (any, error) {
	doc, err := s.readingDocument(r)
	if err != nil {
		return nil, err
	}
	in := struct {
		Ignored bool `json:"ignored"`
	}{Ignored: true}
	if err := readingDecodeOptional(r, &in); err != nil {
		return nil, err
	}
	doc, err = s.readingService().Ignore(r.Context(), doc, in.Ignored)
	if err != nil {
		return nil, err
	}
	return newReadingItemCache(s.Catalog).documentDict(r.Context(), doc), nil
}

// readingAutoLinkAll retries auto-linking for documents it hasn't tried yet.
func (s *Server) readingAutoLinkAll(w http.ResponseWriter, r *http.Request) (any, error) {
	linked, tried, err := s.readingService().AutoLinkPending(r.Context(), auth.User(r.Context()).ID)
	if err != nil {
		return nil, err
	}
	return map[string]int{"linked": linked, "tried": tried}, nil
}

func (s *Server) readingGetNotes(w http.ResponseWriter, r *http.Request) (any, error) {
	id, err := readingPathID(r, "entry_id")
	if err != nil {
		return nil, err
	}
	n, err := s.readingService().GetNotes(r.Context(), auth.User(r.Context()).ID, id)
	if err != nil {
		return nil, readingErr(err)
	}
	return n, nil
}

func (s *Server) readingPutNotes(w http.ResponseWriter, r *http.Request) (any, error) {
	id, err := readingPathID(r, "entry_id")
	if err != nil {
		return nil, err
	}
	var raw map[string]json.RawMessage
	if err := DecodeJSON(r, &raw); err != nil {
		return nil, err
	}
	var u reading.NotesUpdate
	text := map[string]**string{"preview": new(*string), "predict": new(*string), "purpose_note": new(*string), "reflect": new(*string)}
	for name, dst := range text {
		v, ok := raw[name]
		if !ok {
			continue
		}
		var str string
		if err := json.Unmarshal(v, &str); err != nil || string(v) == "null" {
			return nil, Errorf(http.StatusUnprocessableEntity, name+" must be a string")
		}
		*dst = &str
	}
	u.Preview, u.Predict, u.PurposeNote, u.Reflect = *text["preview"], *text["predict"], *text["purpose_note"], *text["reflect"]
	if v, ok := raw["purpose"]; ok {
		u.PurposeSet = true
		if string(v) != "null" {
			var p string
			if err := json.Unmarshal(v, &p); err != nil {
				return nil, Errorf(http.StatusUnprocessableEntity, "purpose must be a string or null")
			}
			u.Purpose = &p
		}
	}
	if err := reading.ValidateNotes(u); err != nil {
		return nil, Errorf(http.StatusUnprocessableEntity, strings.TrimPrefix(err.Error(), reading.ErrInvalid.Error()+": "))
	}
	n, err := s.readingService().UpdateNotes(r.Context(), auth.User(r.Context()).ID, id, u)
	if err != nil {
		return nil, readingErr(err)
	}
	return n, nil
}

// readingDays is the daily reading log: per local day, time and pages,
// merged per book (GET /api/v1/reading/days?start=&end=, default 30 days).
func (s *Server) readingDays(w http.ResponseWriter, r *http.Request) (any, error) {
	svc := s.readingService()
	start, end, err := s.readingDateRange(r, svc)
	if err != nil {
		return nil, readingErr(err)
	}
	return svc.Journal(r.Context(), auth.User(r.Context()).ID, start, end)
}

// readingUploadTOC stores the table of contents of the book open in
// KOReader: {md5, title, authors, total_pages, toc: [{title, page, depth}]}.
func (s *Server) readingUploadTOC(w http.ResponseWriter, r *http.Request) (any, error) {
	var in struct {
		MD5        string `json:"md5"`
		Title      string `json:"title"`
		Authors    string `json:"authors"`
		TotalPages int64  `json:"total_pages"`
		TOC        []struct {
			Title string `json:"title"`
			Page  int64  `json:"page"`
			Depth int    `json:"depth"`
		} `json:"toc"`
	}
	if err := DecodeJSON(r, &in); err != nil {
		return nil, err
	}
	if in.MD5 == "" || in.TotalPages <= 0 {
		return nil, Errorf(http.StatusUnprocessableEntity, "md5 and total_pages are required")
	}
	if len(in.TOC) > 5000 {
		return nil, Errorf(http.StatusUnprocessableEntity, "toc is too long")
	}
	entries := make([]reading.TOCEntry, len(in.TOC))
	for i, e := range in.TOC {
		entries[i] = reading.TOCEntry{Title: e.Title, Page: e.Page, Depth: e.Depth}
	}
	tp := in.TotalPages
	f := reading.DocFields{Title: in.Title, Authors: in.Authors, TotalPages: &tp}
	if err := s.readingService().SaveTOC(r.Context(), auth.User(r.Context()).ID, in.MD5, f, in.TotalPages, entries); err != nil {
		return nil, err
	}
	return map[string]any{"chapters": len(entries)}, nil
}

// readingChapters: the chapter each book being read is in, keyed by item id
// ({"123": {"chapter": "Chapter 12", "page": 214}}).
func (s *Server) readingChapters(w http.ResponseWriter, r *http.Request) (any, error) {
	return s.readingService().Chapters(r.Context(), auth.User(r.Context()).ID)
}
