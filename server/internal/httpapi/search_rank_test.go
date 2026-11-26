package httpapi

import (
	"slices"
	"testing"
)

func TestRankByTitle(t *testing.T) {
	var results []map[string]any
	for _, title := range []string{"Labyrinth of Peace", "Sousou no Frieren", "Frieren: Beyond Journey's End", "Frierenburg", "FRIEREN"} {
		results = append(results, map[string]any{"title": title})
	}
	rankByTitle(results, "frieren")
	var got []string
	for _, r := range results {
		got = append(got, r["title"].(string))
	}
	want := []string{"FRIEREN", "Frieren: Beyond Journey's End", "Sousou no Frieren", "Frierenburg", "Labyrinth of Peace"}
	if !slices.Equal(got, want) {
		t.Fatalf("got %q, want %q", got, want)
	}
}
