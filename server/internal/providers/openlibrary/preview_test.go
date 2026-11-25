package openlibrary

import "testing"

func TestPreviewText(t *testing.T) {
	toc := []TOCEntry{{"Part One", 0}, {"Chapter 1", 1}, {"", 0}}
	cases := []struct {
		name, synopsis string
		toc            []TOCEntry
		want           string
	}{
		{"both", "A story.", toc, "A story.\n\nContents:\n  Part One\n    Chapter 1"},
		{"synopsis only", " A story. ", nil, "A story."},
		{"toc only", "", toc, "Contents:\n  Part One\n    Chapter 1"},
		{"placeholder", "No synopsis available.", toc[:1], "Contents:\n  Part One"},
		{"nothing", "", nil, ""},
	}
	for _, c := range cases {
		if got := PreviewText(c.synopsis, c.toc); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}
