package catalog_test

import (
	"context"
	"errors"
	"testing"

	"github.com/the-agency-labs/encompass/server/internal/catalog"
	"github.com/the-agency-labs/encompass/server/internal/providers"
)

type searchMAL struct {
	hits []providers.Metadata
	err  error
}

func (f searchMAL) Anime(context.Context, string) (catalog.Metadata, error) { return nil, nil }
func (f searchMAL) Manga(context.Context, string) (catalog.Metadata, error) { return nil, nil }
func (f searchMAL) Search(context.Context, string, string, int, bool) (catalog.SearchPage, error) {
	return page(f.hits), f.err
}

type searchMU struct {
	hits []providers.Metadata
	err  error
}

func (f searchMU) Manga(context.Context, string) (catalog.Metadata, error) { return nil, nil }
func (f searchMU) Search(context.Context, string, int, bool) (catalog.SearchPage, error) {
	return page(f.hits), f.err
}

func page(hits []providers.Metadata) catalog.SearchPage {
	if hits == nil {
		return nil
	}
	return catalog.SearchPage{"page": 1, "total_results": len(hits), "results": hits}
}

func hit(source, title string, aliases ...string) providers.Metadata {
	return providers.Metadata{"source": source, "media_type": "manga", "title": title, "aliases": aliases}
}

func titles(t *testing.T, res catalog.SearchPage) []string {
	t.Helper()
	var out []string
	for _, h := range providers.List(res, "results") {
		out = append(out, providers.Str(h, "source")+":"+providers.Str(h, "title"))
	}
	return out
}

func TestMangaSearchMALFirstMangaUpdatesFillsGaps(t *testing.T) {
	mal := searchMAL{hits: []providers.Metadata{
		hit("mal", "Omniscient Reader's Viewpoint", "Jeonjijeok Dokja Sijeom", "Omniscient Reader"),
	}}
	mu := searchMU{hits: []providers.Metadata{
		hit("mangaupdates", "Omniscient Reader", "Omniscient Reader"),                  // MAL has it: hidden
		hit("mangaupdates", "Omniscient Reader's Viewpoint (Novel)"),                   // same name once "(Novel)" is dropped
		hit("mangaupdates", "The Beginning After the End"),                             // unrelated to the query words
		hit("mangaupdates", "Omniscient Reader: Side Story", "Omniscient Reader Side"), // MAL lacks it: added
	}}
	reg := catalog.NewRegistry(catalog.Providers{MAL: mal, MangaUpdates: mu}, nil)
	res, err := reg.Search(context.Background(), catalog.KindManga, "", "omniscient reader", 1, false)
	if err != nil {
		t.Fatal(err)
	}
	got := titles(t, res)
	want := []string{"mal:Omniscient Reader's Viewpoint", "mangaupdates:Omniscient Reader: Side Story"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("results = %v, want %v", got, want)
	}
}

func TestMangaSearchFindsWhatMALLacks(t *testing.T) {
	mu := searchMU{hits: []providers.Metadata{
		hit("mangaupdates", "The Beginning After the End"),
		hit("mangaupdates", "After the Beginning"), // MU's loose match: not every query word
	}}
	reg := catalog.NewRegistry(catalog.Providers{MAL: searchMAL{hits: []providers.Metadata{}}, MangaUpdates: mu}, nil)
	res, err := reg.Search(context.Background(), catalog.KindManga, "", "the beginning after the end", 1, false)
	if err != nil {
		t.Fatal(err)
	}
	if got := titles(t, res); len(got) != 1 || got[0] != "mangaupdates:The Beginning After the End" {
		t.Fatalf("results = %v", got)
	}
	// Either provider failing leaves the other's results.
	reg = catalog.NewRegistry(catalog.Providers{MAL: searchMAL{err: errors.New("down")}, MangaUpdates: mu}, nil)
	if res, err := reg.Search(context.Background(), catalog.KindManga, "", "the beginning after the end", 1, false); err != nil || len(providers.List(res, "results")) == 0 {
		t.Fatalf("MAL down: %v %v", res, err)
	}
}
