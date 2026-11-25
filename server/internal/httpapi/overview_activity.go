package httpapi

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/the-agency-labs/encompass/server/internal/catalog"
	"github.com/the-agency-labs/encompass/server/internal/library"
)

// overviewVerb is the activity of a kind: watch, read or play.
func overviewVerb(kind string) string {
	switch kind {
	case catalog.KindManga, catalog.KindBook, catalog.KindComic:
		return "read"
	case catalog.KindGame, catalog.KindBoardgame:
		return "play"
	}
	return "watch"
}

func overviewUnit(kind string, plural bool) string {
	unit := map[string]string{
		catalog.KindTV: "episode", catalog.KindAnime: "episode", catalog.KindMovie: "episode", catalog.KindAnimeMovie: "episode",
		catalog.KindManga: "chapter", catalog.KindBook: "page", catalog.KindComic: "issue",
		catalog.KindGame: "minute", catalog.KindBoardgame: "play",
	}[kind]
	if plural {
		unit += "s"
	}
	return unit
}

func overviewHHMM(minutes int) string {
	if minutes < 0 {
		minutes = -minutes
	}
	return fmt.Sprintf("%d:%02d", minutes/60, minutes%60)
}

var overviewInitialStatus = map[string]map[string]string{
	"In progress": {"watch": "Marked as currently watching", "read": "Marked as currently reading", "play": "Marked as currently playing"},
	"Completed":   {"watch": "Marked as finished watching", "read": "Marked as finished reading", "play": "Marked as finished playing"},
	"Planning":    {"watch": "Added to watching list", "read": "Added to reading list", "play": "Added to playing list"},
	"Paused":      {"watch": "Marked as paused watching", "read": "Marked as paused reading", "play": "Marked as paused playing"},
}

var overviewStatusTransition = map[[2]string]map[string]string{
	{"Planning", "In progress"}:  {"watch": "Currently watching", "read": "Currently reading", "play": "Currently playing"},
	{"In progress", "Completed"}: {"watch": "Finished watching", "read": "Finished reading", "play": "Finished playing"},
	{"In progress", "Paused"}:    {"watch": "Paused watching", "read": "Paused reading", "play": "Paused playing"},
	{"Paused", "In progress"}:    {"watch": "Resumed watching", "read": "Resumed reading", "play": "Resumed playing"},
	{"In progress", "Dropped"}:   {"watch": "Stopped watching", "read": "Stopped reading", "play": "Stopped playing"},
}

// overviewChange renders one entry_events change in the shape of the Django
// journal: {"description", "field", "new"}.
func overviewChange(kind string, c library.Change) map[string]any {
	verb := overviewVerb(kind)
	switch c.Field {
	case "created", "status":
		field := "status"
		var desc string
		switch {
		case c.New == "Dropped" && c.Old == "":
			desc = "Marked as dropped"
		case c.Old == "":
			desc = overviewInitialStatus[c.New][verb]
		case overviewStatusTransition[[2]string{c.Old, c.New}] != nil:
			desc = overviewStatusTransition[[2]string{c.Old, c.New}][verb]
		default:
			desc = fmt.Sprintf("Changed status from %s to %s", c.Old, c.New)
		}
		if desc == "" {
			desc = "Set status to " + c.New
		}
		return map[string]any{"description": desc, "field": field, "new": c.New}
	case "score":
		if c.New == "" {
			return map[string]any{"description": "Removed rating", "field": "score", "new": nil}
		}
		score, _ := strconv.ParseFloat(c.New, 64)
		old, _ := strconv.ParseFloat(c.Old, 64)
		desc := fmt.Sprintf("Rated %s/10", strconv.FormatFloat(score, 'f', 1, 64))
		if c.Old != "" && old != 0 {
			desc = fmt.Sprintf("Changed rating from %s to %s", strconv.FormatFloat(old, 'f', -1, 64), strconv.FormatFloat(score, 'f', -1, 64))
		}
		return map[string]any{"description": desc, "field": "score", "new": score}
	case "progress":
		n, _ := strconv.Atoi(c.New)
		old, _ := strconv.Atoi(c.Old)
		var desc string
		switch {
		case kind == catalog.KindGame && c.Old == "":
			desc = "Played for " + overviewHHMM(n)
		case kind == catalog.KindGame && n >= old:
			desc = fmt.Sprintf("Added %s of playtime", overviewHHMM(n-old))
		case kind == catalog.KindGame:
			desc = fmt.Sprintf("Removed %s of playtime", overviewHHMM(n-old))
		case c.Old == "":
			title := map[string]string{"watch": "Watched", "read": "Read", "play": "Played"}[verb]
			desc = fmt.Sprintf("%s up to %s %d", title, overviewUnit(kind, false), n)
		default:
			desc = fmt.Sprintf("Progress set to %d %s", n, overviewUnit(kind, n != 1))
		}
		return map[string]any{"description": desc, "field": "progress", "new": n}
	}
	return map[string]any{"description": fmt.Sprintf("Updated %s", c.Field), "field": c.Field, "new": c.New}
}

// overviewActivityEntry shapes one row of the feed.
func overviewActivityEntry(a library.ActivityEntry) map[string]any {
	if a.Kind == "watch" {
		if a.Episode == nil {
			return map[string]any{
				"date": a.At, "media_type": a.Item.Kind, "item": overviewItem(a.Item, "", nil, nil),
				"changes": []map[string]any{{"description": "Watched", "field": "end_date"}},
			}
		}
		return map[string]any{
			"date": a.At, "media_type": "episode", "item": overviewItem(a.Item, "episode", a.Season, a.Episode),
			"changes": []map[string]any{{"description": fmt.Sprintf("Watched episode %d", *a.Episode), "field": "end_date"}},
		}
	}
	changes := make([]map[string]any, 0, len(a.Changes))
	for _, c := range a.Changes {
		changes = append(changes, overviewChange(a.Item.Kind, c))
	}
	return map[string]any{"date": a.At, "media_type": a.Item.Kind, "item": overviewItem(a.Item, "", nil, nil), "changes": changes}
}

func overviewFormatCursor(c library.ActivityCursor) string {
	return fmt.Sprintf("%s|%d|%d", c.At, c.Rank, c.ID)
}

func overviewParseCursor(s string) (*library.ActivityCursor, error) {
	parts := strings.Split(s, "|")
	if len(parts) != 3 {
		return nil, Errorf(http.StatusUnprocessableEntity, "Invalid cursor")
	}
	rank, err1 := strconv.Atoi(parts[1])
	id, err2 := strconv.ParseInt(parts[2], 10, 64)
	if err1 != nil || err2 != nil || (rank != 0 && rank != 1) {
		return nil, Errorf(http.StatusUnprocessableEntity, "Invalid cursor")
	}
	return &library.ActivityCursor{At: parts[0], Rank: rank, ID: id}, nil
}

func (s *Server) overviewActivity(r *http.Request) (any, error) {
	u := overviewUser(r.Context())
	loc := overviewLoc(&u)
	q := r.URL.Query()
	limit, err := overviewIntParam(q, "limit", 50)
	if err != nil {
		return nil, err
	}
	aq := library.ActivityQuery{Limit: overviewClamp(limit, 1, 200)}
	for _, p := range []struct {
		name string
		dst  **time.Time
		plus int
	}{{"start", &aq.From, 0}, {"end", &aq.To, 1}} {
		v := q.Get(p.name)
		if v == "" {
			continue
		}
		d, err := time.ParseInLocation("2006-01-02", v, loc)
		if err != nil {
			return nil, Errorf(http.StatusUnprocessableEntity, p.name+" must be a date (YYYY-MM-DD)")
		}
		d = d.AddDate(0, 0, p.plus) // end is inclusive: stop before the next day
		*p.dst = &d
	}
	if c := q.Get("cursor"); c != "" {
		if aq.Cursor, err = overviewParseCursor(c); err != nil {
			return nil, err
		}
	}
	page, err := s.Library.Activity(r.Context(), u.ID, aq)
	if err != nil {
		return nil, err
	}
	enabled, err := s.overviewEnabled(r.Context(), u.ID)
	if err != nil {
		return nil, err
	}
	entries := []map[string]any{}
	for _, a := range page.Entries {
		if enabled[a.Item.Kind] {
			entries = append(entries, overviewActivityEntry(a))
		}
	}
	var next any
	if page.Next != nil {
		next = overviewFormatCursor(*page.Next)
	}
	return map[string]any{"entries": entries, "next_cursor": next}, nil
}
