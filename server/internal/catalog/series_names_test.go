package catalog_test

import (
	"testing"

	"github.com/the-agency-labs/encompass/server/internal/catalog"
	"github.com/the-agency-labs/encompass/server/internal/catalog/catalogtest"
)

func TestNormalizeSeriesNameKeepsDistinguishingPart(t *testing.T) {
	for in, want := range map[string]string{
		"The Red Rising Trilogy":                  "Red Rising",
		"Red Rising Saga":                         "Red Rising",
		"The Wheel of Time":                       "The Wheel of Time",
		"The Mistborn Saga: The Original Trilogy": "Mistborn: Original Trilogy",
		"Mistborn: Era One":                       "Mistborn: Era One",
		"The Foo Saga: The Wax and Wayne Series":  "Foo: Wax and Wayne",
		"Discworld - Ankh-Morpork City Watch":     "Discworld - Ankh-Morpork City Watch",
		"The Original Trilogy":                    "The Original Trilogy",
		"Dune Universe (Publication Order)":       "Dune Universe (Publication Order)",
		"Enderverse:  Publication Order":          "Enderverse: Publication Order",
	} {
		if got := catalog.NormalizeSeriesName(in); got != want {
			t.Errorf("NormalizeSeriesName(%q) = %q, want %q", in, got, want)
		}
	}
}

// A reading-order list is never a book's primary series while a regular one
// exists, whatever order the provider lists them in.
func TestMetaSeriesNeverPrimaryWhenARegularOneExists(t *testing.T) {
	e := newSeriesEnv(t)
	e.hc.AddBook("7", "Crossover")
	e.hc.AddSeries("1", "Foo Universe (Chronological)", catalogtest.SeriesBook(23, "7", "Crossover"))
	e.hc.AddSeries("2", "Foo", catalogtest.SeriesBook(2, "7", "Crossover"))
	item, err := e.Catalog.EnsureItem(ctx, catalog.KindBook, catalog.SourceHardcover, "7")
	if err != nil {
		t.Fatal(err)
	}
	regular, meta := e.seriesByKey(t, "hardcover", "2"), e.seriesByKey(t, "hardcover", "1")
	eq(t, "meta series still stored", len(e.seriesBooks(t, meta.ID)), 1)
	if got := catalog.ItemSeriesFromMeta(item.Meta); got == nil || got.ID != regular.ID || got.Position != 2 {
		t.Fatalf("primary = %+v", got)
	}

	// Alone, a meta list is the primary series.
	e.hc.AddBook("8", "Solo")
	e.hc.AddSeries("3", "Bar Universe", catalogtest.SeriesBook(1, "8", "Solo"))
	solo, err := e.Catalog.EnsureItem(ctx, catalog.KindBook, catalog.SourceHardcover, "8")
	if err != nil {
		t.Fatal(err)
	}
	if got := catalog.ItemSeriesFromMeta(solo.Meta); got == nil || got.Name != "Bar Universe" {
		t.Fatalf("solo primary = %+v", got)
	}
}
