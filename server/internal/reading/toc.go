package reading

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"sort"
	"strings"

	"github.com/the-agency-labs/axiom/server/internal/db"
	"github.com/the-agency-labs/axiom/server/internal/library"
)

// TOCEntry is one line of a book's table of contents as KOReader lists it:
// the page it starts on (in KOReader's current layout) and its nesting.
type TOCEntry struct {
	Title string
	Page  int64
	Depth int
}

// tocChapter is a stored chapter: where it starts as a fraction of the book.
type tocChapter struct {
	Title string  `json:"title"`
	Start float64 `json:"start"`
	Depth int     `json:"depth"`
}

// SaveTOC stores the table of contents KOReader sent for a book (by its
// partial md5), replacing the previous one. pages is KOReader's page count
// for the layout the entries' pages belong to.
func (s *Service) SaveTOC(ctx context.Context, userID int64, md5 string, f DocFields, pages int64, entries []TOCEntry) error {
	if pages <= 0 {
		return errors.New("pages must be positive")
	}
	chapters := make([]tocChapter, 0, len(entries))
	for _, e := range entries {
		title := strings.Join(strings.Fields(e.Title), " ")
		if title == "" || e.Page < 1 {
			continue
		}
		start := float64(e.Page-1) / float64(pages)
		chapters = append(chapters, tocChapter{Title: title, Start: min(max(start, 0), 1), Depth: max(e.Depth, 1)})
	}
	// By position; at one position the outer entry first, so the deepest wins.
	sort.SliceStable(chapters, func(i, j int) bool {
		if chapters[i].Start != chapters[j].Start {
			return chapters[i].Start < chapters[j].Start
		}
		return chapters[i].Depth < chapters[j].Depth
	})
	raw, err := json.Marshal(chapters)
	if err != nil {
		return err
	}
	return s.tx(ctx, func(q *db.Queries) error {
		doc, _, err := getOrCreateDocument(ctx, q, userID, md5, f, s.now())
		if err != nil {
			return err
		}
		return q.UpsertDocumentTOC(ctx, db.UpsertDocumentTOCParams{DocumentID: doc.ID, Chapters: string(raw), UpdatedAt: Stamp(s.now())})
	})
}

// ChapterAt is the chapter a book in progress is in, at the page the library
// shows for it.
type ChapterAt struct {
	Chapter string `json:"chapter"`
	Page    int64  `json:"page"`
}

// Chapters returns, for every book the user is reading (or paused) that has
// a table of contents, the chapter at its current page, keyed by item id.
func (s *Service) Chapters(ctx context.Context, userID int64) (map[int64]ChapterAt, error) {
	rows, err := s.Q.ListUserTOCs(ctx, userID)
	if err != nil {
		return nil, err
	}
	out := map[int64]ChapterAt{}
	seen := map[int64]bool{}
	for _, r := range rows {
		if seen[r.ItemID] { // newest table of contents per book
			continue
		}
		seen[r.ItemID] = true
		if s.Library == nil {
			continue
		}
		entry, err := s.Library.CurrentEntry(ctx, userID, r.ItemID)
		if errors.Is(err, library.ErrNoEntry) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if (entry.Status != library.InProgress && entry.Status != library.Paused) || entry.Progress <= 0 {
			continue
		}
		item, err := s.Catalog.GetItem(ctx, r.ItemID)
		if err != nil {
			return nil, err
		}
		total := item.Total.Int64
		if total <= 0 {
			total = r.TotalPages.Int64
		}
		if total <= 0 {
			continue
		}
		var chapters []tocChapter
		if json.Unmarshal([]byte(r.Chapters), &chapters) != nil {
			continue
		}
		if c := chapterAt(chapters, float64(entry.Progress)/float64(total)); c != "" {
			out[r.ItemID] = ChapterAt{Chapter: c, Page: entry.Progress}
		}
	}
	return out, nil
}

// chapterAt names the chapter that position (0..1) falls in: the last entry
// starting before it, the deepest one when several start together — what
// KOReader shows in its footer. Being exactly on a chapter's start counts as
// the end of the previous one (the reader is on that chapter's last page).
func chapterAt(chapters []tocChapter, pos float64) string {
	var hit *tocChapter
	for i := range chapters {
		if chapters[i].Start < pos || chapters[i].Start == 0 {
			hit = &chapters[i]
		}
	}
	if hit == nil {
		return ""
	}
	return chapterLabel(hit.Title)
}

var (
	chapterWord = regexp.MustCompile(`(?i)^(?:chapter|chap\.?|ch\.?)\s*([0-9]+|[ivxlcdm]+\b|[a-z-]+)`)
	leadNumber  = regexp.MustCompile(`^([0-9]+)(?:$|[\s.:)\-–—])`)
)

// chapterLabel turns a table-of-contents title into "Chapter N" when it is a
// numbered chapter ("Chapter 12: The Gate", "Chapter iv", "Chapter Ten",
// "12. The Gate", "12"); any other title (Prologue, Part One, Interlude) is
// kept as it is, shortened.
func chapterLabel(title string) string {
	if m := chapterWord.FindStringSubmatch(title); m != nil {
		n := m[1]
		switch {
		case digits.MatchString(n):
		case roman.MatchString(n):
			n = strings.ToUpper(n)
		default:
			n = strings.ToUpper(n[:1]) + n[1:]
		}
		return "Chapter " + n
	}
	if m := leadNumber.FindStringSubmatch(title); m != nil {
		return "Chapter " + m[1]
	}
	if r := []rune(title); len(r) > 32 {
		return strings.TrimSpace(string(r[:31])) + "…"
	}
	return title
}

var (
	digits = regexp.MustCompile(`^[0-9]+$`)
	roman  = regexp.MustCompile(`(?i)^[ivxlcdm]+$`)
)
