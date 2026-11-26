package reading

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/the-agency-labs/axiom/server/internal/catalog"
	"github.com/the-agency-labs/axiom/server/internal/db"
	"github.com/the-agency-labs/axiom/server/internal/providers"
)

const (
	// SuggestionLimit is how many suggestions the documents list carries per book.
	SuggestionLimit = 5
	// maxAuthorChecks bounds the metadata lookups made to verify authors of
	// exact-title candidates.
	maxAuthorChecks = 4
)

var providerAuthorSplit = regexp.MustCompile(`\s*(?:,|;|&|\band\b|\n)\s*`)

// resultID reads a search result's media_id (a string, or a number from JSON).
func resultID(m map[string]any) string {
	switch v := m["media_id"].(type) {
	case string:
		return v
	case float64:
		return strconv.FormatInt(int64(v), 10)
	case int:
		return strconv.Itoa(v)
	case int64:
		return strconv.FormatInt(v, 10)
	}
	return ""
}

func resultSource(m map[string]any, fallback string) string {
	if s, ok := m["source"].(string); ok && s != "" {
		return s
	}
	return fallback
}

// query builds the provider search text for a parsed document: the title
// and the first author.
func (p Parsed) query() string {
	q := p.Query
	if len(p.Authors) > 0 {
		q += " " + p.Authors[0]
	}
	return strings.TrimSpace(q)
}

func (s *Service) search(ctx context.Context, source, query string) ([]any, error) {
	page, err := s.Catalog.Reg.Search(ctx, catalog.KindBook, source, query, 1, false)
	if err != nil {
		if errors.Is(err, catalog.ErrUnsupported) {
			return nil, fmt.Errorf("%w: %s", ErrUnsupported, source)
		}
		return nil, fmt.Errorf("%w: %v", ErrProvider, err)
	}
	list, _ := page["results"].([]any)
	return list, nil
}

// Suggestions returns book search results a document could be linked to,
// best match first. The provider's whole result list is returned, re-ranked:
// exact title matches first, close titles next, study guides, notebooks and
// the like last.
func (s *Service) Suggestions(ctx context.Context, doc Document, source string) ([]any, error) {
	p := ParseDocument(doc.Title, doc.Authors)
	q := p.query()
	if q == "" {
		return []any{}, nil
	}
	results, err := s.search(ctx, source, q)
	if err != nil {
		return nil, err
	}
	if len(results) == 0 && len(p.Authors) > 0 {
		if results, err = s.search(ctx, source, p.Query); err != nil {
			return nil, err
		}
	}
	return Rank(results, p, false), nil
}

// TopSuggestions is Suggestions cut to the few that are plausible.
func (s *Service) TopSuggestions(ctx context.Context, doc Document) ([]any, error) {
	p := ParseDocument(doc.Title, doc.Authors)
	all, err := s.Suggestions(ctx, doc, "")
	if err != nil {
		return nil, err
	}
	top := Rank(all, p, true)
	if len(top) > SuggestionLimit {
		top = top[:SuggestionLimit]
	}
	return top, nil
}

// Match is a high-confidence match of a document with a provider book.
type Match struct {
	Source  string
	MediaID string
	Title   string
}

// FindMatch looks for a book whose normalized title equals one of the
// document's title candidates and whose author equals one of its authors.
// It returns false when there is no such book (or the document has no
// author to verify): a near miss is only ever a suggestion.
func (s *Service) FindMatch(ctx context.Context, doc Document) (Match, bool, error) {
	p := ParseDocument(doc.Title, doc.Authors)
	var firstErr error
	for _, v := range []*Parsed{&p, p.Guess} {
		if v == nil || len(v.Titles) == 0 || len(v.Authors) == 0 {
			continue
		}
		m, ok, err := s.matchVariant(ctx, *v)
		if ok {
			return m, true, nil
		}
		if err != nil {
			firstErr = err
		}
	}
	return Match{}, false, firstErr
}

// matchVariant searches the providers for a title-and-author match of one
// reading of the document.
func (s *Service) matchVariant(ctx context.Context, p Parsed) (Match, bool, error) {
	var firstErr error
	failed := 0
	sources := []string{"", catalog.SourceOpenLibrary}
	for _, src := range sources {
		results, err := s.search(ctx, src, p.query())
		if errors.Is(err, ErrUnsupported) {
			failed++
			continue
		}
		if err != nil {
			failed++
			firstErr = err
			continue
		}
		fallbackSource := src
		if fallbackSource == "" {
			fallbackSource = catalog.SourceHardcover
		}
		checked := 0
		for _, r := range Rank(results, p, true) {
			m, _ := r.(map[string]any)
			if TitleScore(providers.Str(m, "title"), p) < 100 || checked >= maxAuthorChecks {
				break
			}
			checked++
			id, source := resultID(m), resultSource(m, fallbackSource)
			if id == "" {
				continue
			}
			meta, err := s.Catalog.Reg.Metadata(ctx, catalog.KindBook, source, id)
			if err != nil {
				firstErr = fmt.Errorf("%w: %v", ErrProvider, err)
				continue
			}
			if AuthorsMatch(p.Authors, metaAuthors(meta)) {
				return Match{Source: source, MediaID: id, Title: providers.Str(m, "title")}, true, nil
			}
		}
	}
	if failed == len(sources) && firstErr != nil {
		return Match{}, false, firstErr
	}
	return Match{}, false, nil
}

// metaAuthors reads the author(s) out of a book's provider metadata.
func metaAuthors(meta providers.Metadata) []string {
	details, _ := meta["details"].(map[string]any)
	var out []string
	add := func(raw string) {
		if n := NormalizeAuthor(raw); n != "" && !containsStr(out, n) {
			out = append(out, n)
		}
	}
	var raws []string
	switch a := details["author"].(type) {
	case string:
		raws = []string{a}
	case []any:
		for _, e := range a {
			if str, ok := e.(string); ok {
				raws = append(raws, str)
			}
		}
	}
	for _, r := range raws {
		add(r) // "Last, First" as a whole
		for _, part := range providerAuthorSplit.Split(r, -1) {
			add(part)
		}
	}
	return out
}

// AutoLink links an unlinked document when FindMatch finds a high-confidence
// match. It records the attempt so uploads don't repeat it, except when the
// providers were unreachable.
func (s *Service) AutoLink(ctx context.Context, doc Document) (Document, bool, error) {
	if doc.ItemID.Valid || doc.MergedInto.Valid || doc.Ignored != 0 {
		return doc, false, nil
	}
	if IsStockDocument(doc.Title) {
		doc, err := s.Ignore(ctx, doc, true)
		return doc, false, err
	}
	m, ok, err := s.FindMatch(ctx, doc)
	if err != nil {
		return doc, false, err
	}
	if !ok {
		now := nstamp(s.now())
		if err := s.Q.MarkReadingDocumentChecked(ctx, db.MarkReadingDocumentCheckedParams{LinkCheckedAt: now, ID: doc.ID}); err != nil {
			return doc, false, err
		}
		doc.LinkCheckedAt = now
		return doc, false, nil
	}
	item, err := s.Catalog.EnsureItem(ctx, catalog.KindBook, m.Source, m.MediaID)
	if err != nil {
		return doc, false, fmt.Errorf("%w: %v", ErrProvider, err)
	}
	doc, err = s.LinkDocument(ctx, doc, item)
	if err == nil {
		slog.InfoContext(ctx, "reading: auto-linked document", "document", doc.ID, "item", item.ID)
	}
	return doc, err == nil, err
}

// AutoLinkPending runs AutoLink for the user's documents it hasn't tried yet
// and returns how many got linked and how many were tried.
func (s *Service) AutoLinkPending(ctx context.Context, userID int64) (linked, tried int, err error) {
	docs, err := s.Q.ListReadingDocuments(ctx, userID)
	if err != nil {
		return 0, 0, err
	}
	for _, d := range docs {
		if d.ItemID.Valid || d.MergedInto.Valid || d.Ignored != 0 || d.LinkCheckedAt.Valid {
			continue
		}
		tried++
		_, ok, err := s.AutoLink(ctx, d)
		if err != nil {
			slog.WarnContext(ctx, "reading: auto-link failed", "document", d.ID, "err", err)
			continue
		}
		if ok {
			linked++
		}
	}
	return linked, tried, nil
}

// AutoLinkInBackground tries AutoLink on its own context so an upload isn't
// held up by provider calls.
func (s *Service) AutoLinkInBackground(doc Document) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if _, _, err := s.AutoLink(ctx, doc); err != nil {
		slog.Warn("reading: auto-link failed", "document", doc.ID, "err", err)
	}
}

// LinkToBook links a document to the book a provider identifies (the
// "Link your books" action), creating the item when needed.
func (s *Service) LinkToBook(ctx context.Context, doc Document, source, mediaID string) (Document, error) {
	item, err := s.Catalog.EnsureItem(ctx, catalog.KindBook, source, mediaID)
	if errors.Is(err, catalog.ErrUnsupported) {
		return doc, fmt.Errorf("%w: %s", ErrUnsupported, source)
	}
	if err != nil {
		return doc, fmt.Errorf("%w: %v", ErrProvider, err)
	}
	return s.LinkDocument(ctx, doc, item)
}
