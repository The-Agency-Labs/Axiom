package reading

import "testing"

func TestChapterLabel(t *testing.T) {
	for in, want := range map[string]string{
		"Chapter 12: The Gate": "Chapter 12",
		"CHAPTER 3":            "Chapter 3",
		"Chapter iv":           "Chapter IV",
		"Chapter Ten":          "Chapter Ten",
		"12. The Gate":         "Chapter 12",
		"7":                    "Chapter 7",
		"Prologue":             "Prologue",
		"Part One":             "Part One",
		"1984 Was a Good Year for Everyone Who Lived":      "Chapter 1984",
		"A Very Long Interlude Title That Goes On Forever": "A Very Long Interlude Title Tha…",
	} {
		if got := chapterLabel(in); got != want {
			t.Errorf("chapterLabel(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestChapterAt(t *testing.T) {
	// A 100-page layout: Part One (p1) holding Chapter 1 (p1) and Chapter 2 (p11).
	ch := []tocChapter{
		{Title: "Part One", Start: 0, Depth: 1},
		{Title: "Chapter 1", Start: 0, Depth: 2},
		{Title: "Chapter 2", Start: 0.10, Depth: 2},
		{Title: "Epilogue", Start: 0.90, Depth: 1},
	}
	for pos, want := range map[float64]string{
		0:    "Chapter 1", // the deepest entry at the start
		0.05: "Chapter 1",
		0.10: "Chapter 1", // on page 10: chapter 1's last page
		0.11: "Chapter 2", // page 11: chapter 2's first page
		0.95: "Epilogue",
		1:    "Epilogue",
	} {
		if got := chapterAt(ch, pos); got != want {
			t.Errorf("chapterAt(%v) = %q, want %q", pos, got, want)
		}
	}
	if got := chapterAt(nil, 0.5); got != "" {
		t.Errorf("no toc: %q", got)
	}
}
