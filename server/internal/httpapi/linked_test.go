package httpapi

import (
	"context"
	"fmt"
	"testing"

	"github.com/the-agency-labs/axiom/server/internal/library"
)

// A title an app tracks refuses hand-made progress unless the request says the
// user confirmed; score and notes stay free, and unlinked titles are open.
func TestLinkedTitlesGuardProgress(t *testing.T) {
	f := newReadingFixture(t)
	ctx := context.Background()
	f.env.HC.Add(readingtestBook("1", "Dune", "Frank Herbert", 500))
	item, err := f.env.Catalog.EnsureItem(ctx, "book", "hardcover", "1")
	if err != nil {
		t.Fatal(err)
	}
	save := func(body map[string]any) readingResp { return f.api("POST", "/library", body) }
	base := map[string]any{"media_type": "book", "source": "hardcover", "media_id": "1"}
	with := func(k string, v any) map[string]any {
		m := map[string]any{k: v}
		for key, val := range base {
			m[key] = val
		}
		return m
	}
	// Unlinked: progress is yours to set.
	readingWant(t, save(with("progress", 10)), 200)

	doc, _, err := f.env.Reading.GetOrCreateDocument(ctx, f.env.User, "h1", readingDocFields("Dune"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.env.Reading.LinkDocument(ctx, doc, item); err != nil {
		t.Fatal(err)
	}
	r := save(with("status", library.Completed))
	readingWant(t, r, 409)
	if body := r.obj(t); body["code"] != "linked" || fmt.Sprint(body["sources"]) != "[Kindle]" {
		t.Fatalf("refusal = %v", body)
	}
	readingWant(t, save(with("progress", 20)), 409)
	readingWant(t, save(with("score", 9)), 200)
	readingWant(t, save(with("status", library.Paused)), 200)
	entry, err := f.env.Library.CurrentEntry(ctx, f.env.User, item.ID)
	if err != nil {
		t.Fatal(err)
	}
	step := fmt.Sprintf("/api/v1/library/book/%d/step", entry.ID)
	key := map[string]string{"Authorization": "Bearer " + f.key}
	readingWant(t, f.do("POST", step, `{"direction":"up"}`, key), 409)
	key[overrideHeader] = "1"
	readingWant(t, f.do("POST", step, `{"direction":"up"}`, key), 200)
}
