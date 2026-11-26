package catalog

import (
	"testing"

	"github.com/the-agency-labs/axiom/server/internal/providers"
)

func series(name string, books ...string) providers.BookSeries {
	s := providers.SeriesInfo{Name: name}
	for i, b := range books {
		s.Books = append(s.Books, providers.SeriesBook{Position: float64(i + 1), ExternalID: b, Title: b})
	}
	return providers.BookSeries{Series: s}
}

func TestChoosePrimarySeries(t *testing.T) {
	for _, tc := range []struct {
		name string
		list []providers.BookSeries
		want int
	}{
		{"sub-arc named after its first book defers to the saga containing it", []providers.BookSeries{
			series("Iron Gold", "Iron Gold", "Dark Age", "Light Bringer", "Red God"),
			series("Red Rising Saga", "Red Rising", "Golden Son", "Morning Star", "Iron Gold", "Dark Age", "Light Bringer", "Red God"),
		}, 1},
		{"series named after its first book stays primary when nothing contains it", []providers.BookSeries{
			series("The Hunger Games", "The Hunger Games", "Catching Fire", "Mockingjay"),
			series("Panem Universe", "The Hunger Games", "Catching Fire", "Mockingjay", "Ballad"),
		}, 0},
		{"a regular series is not displaced by a larger regular one when not a sub-arc", []providers.BookSeries{
			series("Percy Jackson and the Olympians", "The Lightning Thief", "The Sea of Monsters"),
			series("Camp Half-Blood Chronicles", "The Lightning Thief", "The Sea of Monsters", "The Lost Hero"),
		}, 0},
		{"a '-verse' umbrella is never the parent of a sub-arc", []providers.BookSeries{
			series("Six of Crows", "Six of Crows", "Crooked Kingdom"),
			series("Grishaverse", "Shadow and Bone", "Siege and Storm", "Ruin and Rising", "Six of Crows", "Crooked Kingdom"),
		}, 0},
		{"reading-order list is never primary", []providers.BookSeries{
			series("Dune Universe (Publication Order)", "Dune", "Dune Messiah"),
			series("Dune", "Dune", "Dune Messiah"),
		}, 1},
	} {
		ids := make([]int64, len(tc.list))
		for i := range ids {
			ids[i] = int64(i + 1)
		}
		if got := choosePrimarySeries(tc.list, ids); got != tc.want {
			t.Errorf("%s: got %d, want %d", tc.name, got, tc.want)
		}
	}
}
