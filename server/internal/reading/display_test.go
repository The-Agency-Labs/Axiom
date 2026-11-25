package reading

import "testing"

func TestDisplayName(t *testing.T) {
	for _, tc := range []struct{ title, authors, want string }{
		{"Herbert, Frank - Dune 02 - Dune Messiah", "Herbert, Frank", "Dune 2: Dune Messiah"},
		{"Herbert, Frank - Dune 01 - Dune", "Herbert, Frank", "Dune 1: Dune"},
		{"Dune 03: Dune Messiah", "Frank Herbert", "Dune 3: Dune Messiah"},
		{"Brown, Pierce - Red Rising 04 - Iron Gold", "Brown, Pierce", "Red Rising 4: Iron Gold"},
		{"Golden Son (The Red Rising Trilogy, Book 2)", "Brown, Pierce", "The Red Rising Trilogy 2: Golden Son"},
		{"1984 (AmazonClassics Edition)", "Orwell, George", "1984"},
		{"Circe", "Madeline Miller", "Circe"},
		{"How Countries Go Broke: The Big Cycle", "Ray Dalio", "How Countries Go Broke: The Big Cycle"},
		{"Mistborn Trilogy.epub", "Sanderson, Brandon", "Mistborn Trilogy"},
	} {
		if got := DisplayName(tc.title, tc.authors).String(); got != tc.want {
			t.Errorf("DisplayName(%q) = %q, want %q", tc.title, got, tc.want)
		}
	}
}
