package httpapi

import (
	"context"
	"net/http"
	"slices"
	"strings"
	"sync"
	"unicode"

	"github.com/the-agency-labs/encompass/server/internal/catalog"
	"github.com/the-agency-labs/encompass/server/internal/providers"
)

// searchAllKinds are the kinds "all" searches when the client names none.
var searchAllKinds = catalog.Kinds

// searchAll runs the same query on several kinds at once and interleaves the
// results (the best hit of each kind first), so one search covers everything.
// A kind whose provider fails or is not configured is skipped; only when every
// kind fails is the error returned. Page n is page n of every kind, and
// total_pages is the largest of them.
func (s *Server) searchAll(ctx context.Context, kinds []string, query string, page int) (providers.SearchPage, error) {
	type out struct {
		res providers.SearchPage
		err error
	}
	outs := make([]out, len(kinds))
	var wg sync.WaitGroup
	for i, kind := range kinds {
		wg.Add(1)
		go func() {
			defer wg.Done()
			outs[i].res, outs[i].err = s.Catalog.Reg.Search(ctx, kind, "", query, page, true)
		}()
	}
	wg.Wait()

	lists := make([][]map[string]any, len(kinds))
	totalPages, longest, failed := 1, 0, 0
	var firstErr error
	for i, o := range outs {
		if o.err != nil {
			failed++
			if firstErr == nil {
				firstErr = o.err
			}
			continue
		}
		list := providers.List(o.res, "results")
		for _, r := range list {
			r["media_type"] = kinds[i] // the kind searched, not the provider's label (an anime is "tv" on TMDB)
		}
		lists[i] = list
		longest = max(longest, len(list))
		totalPages = max(totalPages, providers.Int(o.res, "total_pages"))
	}
	if failed == len(kinds) && firstErr != nil {
		return nil, firstErr
	}
	results := make([]map[string]any, 0, longest*len(kinds))
	for pos := 0; pos < longest; pos++ {
		for _, l := range lists {
			if pos < len(l) {
				results = append(results, l[pos])
			}
		}
	}
	rankByTitle(results, query)
	return providers.SearchPage{"page": page, "total_pages": totalPages, "total_results": len(results), "results": results}, nil
}

// rankByTitle moves closer title matches up: exact, then starting with the
// query, then containing every word of it, then the rest (matched by a
// provider on another title). The interleaved order is kept within each group,
// so one kind's loose hits no longer outrank another kind's real ones.
func rankByTitle(results []map[string]any, query string) {
	q := foldTitle(query)
	if q == "" {
		return
	}
	words := strings.Fields(q)
	rank := func(r map[string]any) int {
		title, _ := r["title"].(string)
		t := foldTitle(title)
		switch {
		case t == q:
			return 0
		case strings.HasPrefix(t, q+" "):
			return 1
		}
		for _, w := range words {
			if !strings.Contains(t, w) {
				return 3
			}
		}
		return 2
	}
	slices.SortStableFunc(results, func(a, b map[string]any) int { return rank(a) - rank(b) })
}

// foldTitle lowercases a title and reduces punctuation to single spaces.
func foldTitle(s string) string {
	return strings.Join(strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	}), " ")
}

// searchKinds reads the optional comma-separated "types" filter of an "all"
// search, keeping valid kinds in the order given.
func searchKinds(r *http.Request) []string {
	raw := r.URL.Query().Get("types")
	if raw == "" {
		return searchAllKinds
	}
	var kinds []string
	seen := map[string]bool{}
	for _, k := range strings.Split(raw, ",") {
		if k = strings.TrimSpace(k); libraryValidKind(k) && !seen[k] {
			seen[k] = true
			kinds = append(kinds, k)
		}
	}
	if len(kinds) == 0 {
		return searchAllKinds
	}
	return kinds
}
