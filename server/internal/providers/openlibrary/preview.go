package openlibrary

import (
	"strings"

	"github.com/the-agency-labs/encompass/server/internal/providers"
)

// TOCEntry is one table-of-contents line.
type TOCEntry struct {
	Title string
	Level int
}

// TOCFromMetadata reads the "table_of_contents" key of a book's metadata
// (as produced by this package or hardcover's, which has none).
func TOCFromMetadata(meta providers.Metadata) []TOCEntry {
	var out []TOCEntry
	for _, e := range providers.List(meta, "table_of_contents") {
		if title := providers.Str(e, "title"); title != "" {
			out = append(out, TOCEntry{Title: title, Level: providers.Int(e, "level")})
		}
	}
	return out
}

// PreviewText builds the plain-text Preview a Triple P read-through is
// seeded with: the synopsis, a blank line, then "Contents:" and the table of
// contents indented two spaces plus two per level. Missing parts are left
// out; the "No synopsis available." placeholder counts as missing.
func PreviewText(synopsis string, toc []TOCEntry) string {
	synopsis = strings.TrimSpace(synopsis)
	if synopsis == "No synopsis available." {
		synopsis = ""
	}
	var lines []string
	for _, e := range toc {
		title := strings.TrimSpace(e.Title)
		if title == "" {
			continue
		}
		lines = append(lines, strings.Repeat("  ", max(e.Level, 0)+1)+title)
	}
	var b strings.Builder
	b.WriteString(synopsis)
	if len(lines) > 0 {
		if synopsis != "" {
			b.WriteString("\n\n")
		}
		b.WriteString("Contents:\n")
		b.WriteString(strings.Join(lines, "\n"))
	}
	return b.String()
}
