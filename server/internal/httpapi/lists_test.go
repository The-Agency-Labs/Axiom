package httpapi

import (
	"context"
	"fmt"
	"testing"

	"github.com/the-agency-labs/encompass/server/internal/catalog/catalogtest"
	"github.com/the-agency-labs/encompass/server/internal/db"
	"github.com/the-agency-labs/encompass/server/internal/library"
)

type listFx struct {
	*overviewFx
	bob, carol *client // collaborator-to-be and stranger
}

func newListFx(t *testing.T) *listFx {
	f := &listFx{overviewFx: newOverviewFx(t)}
	for _, name := range []string{"bob", "carol"} {
		if _, err := f.s.Auth.CreateUser(context.Background(), name, "password-"+name, false); err != nil {
			t.Fatal(err)
		}
	}
	f.bob, f.carol = f.login("bob", "password-bob"), f.login("carol", "password-carol")
	return f
}

func (f *listFx) call(c *client, method, path string, body any, want int) map[string]any {
	f.t.Helper()
	code, obj, _ := c.do(method, path, body, nil)
	if code != want {
		f.t.Fatalf("%s %s: %d, want %d (%v)", method, path, code, want, obj)
	}
	return obj
}

func (f *listFx) create(name string, extra map[string]any) int {
	f.t.Helper()
	body := map[string]any{"name": name}
	for k, v := range extra {
		body[k] = v
	}
	return num(f.call(f.c, "POST", "/api/v1/lists", body, 200)["id"])
}

func lpath(id int, rest string) string { return fmt.Sprintf("/api/v1/lists/%d%s", id, rest) }

func itemTitles(l map[string]any) []string {
	var out []string
	for _, v := range l["items"].([]any) {
		out = append(out, v.(map[string]any)["item"].(map[string]any)["title"].(string))
	}
	return out
}

func TestListsCRUD(t *testing.T) {
	f := newListFx(t)
	l := f.call(f.c, "POST", "/api/v1/lists", map[string]any{"name": "  Favourites ", "description": "best, of"}, 200)
	id := num(l["id"])
	if l["name"] != "Favourites" || l["visibility"] != "private" || l["is_owner"] != true || l["owner"].(map[string]any)["username"] != "alice" {
		t.Errorf("created = %v", l)
	}
	f.call(f.c, "POST", "/api/v1/lists", map[string]any{"name": " "}, 422)
	f.call(f.c, "POST", "/api/v1/lists", map[string]any{"name": "x", "visibility": "secret"}, 422)
	f.call(f.c, "POST", "/api/v1/lists", map[string]any{}, 422)

	p := f.call(f.c, "PATCH", lpath(id, ""), map[string]any{"name": "Best", "visibility": "public"}, 200)
	if p["name"] != "Best" || p["description"] != "best, of" || p["visibility"] != "public" {
		t.Errorf("patched = %v", p)
	}
	f.create("Another", nil)
	code, _, arr := f.c.do("GET", "/api/v1/lists", nil, nil)
	if code != 200 || len(arr) != 2 {
		t.Fatalf("index: %d %v", code, arr)
	}
	_, _, byName := f.c.do("GET", "/api/v1/lists?sort=name", nil, nil)
	if byName[0].(map[string]any)["name"] != "Another" {
		t.Errorf("sort=name = %v", byName)
	}
	_, _, search := f.c.do("GET", "/api/v1/lists?q=best", nil, nil)
	if len(search) != 1 {
		t.Errorf("q = %v", search)
	}
	f.call(f.c, "DELETE", lpath(id, ""), nil, 204)
	f.call(f.c, "GET", lpath(id, ""), nil, 404)
	f.call(f.c, "GET", "/api/v1/lists/abc", nil, 422)

	if code, _, _ := newClient(t, f.ts.URL).do("GET", "/api/v1/lists", nil, nil); code != 401 {
		t.Errorf("anon: %d", code)
	}
	// Read tokens read but do not write.
	rd := f.token("read")
	if code, _, _ := newClient(t, f.ts.URL).do("GET", "/api/v1/lists", nil, rd); code != 200 {
		t.Errorf("read token GET: %d", code)
	}
	if code, _, _ := newClient(t, f.ts.URL).do("POST", "/api/v1/lists", map[string]any{"name": "x"}, rd); code != 403 {
		t.Errorf("read token POST: %d", code)
	}
	if code, _, _ := newClient(t, f.ts.URL).do("POST", "/api/v1/lists", map[string]any{"name": "via token"}, f.token("write")); code != 200 {
		t.Errorf("write token POST: %d", code)
	}
}

func TestListsPermissions(t *testing.T) {
	f := newListFx(t)
	id := f.create("Shared", nil)
	movie := f.movie("300", "Plain Movie")
	f.call(f.c, "POST", lpath(id, "/items"), map[string]any{"item_id": movie.ID}, 200)

	// A stranger sees nothing of a private list.
	for _, r := range [][2]string{{"GET", ""}, {"PATCH", ""}, {"DELETE", ""}, {"POST", "/items"}, {"DELETE", "/items"}, {"POST", "/members"}, {"PUT", "/order"}} {
		f.call(f.carol, r[0], lpath(id, r[1]), map[string]any{"name": "x", "item_id": movie.ID, "username": "carol"}, 404)
	}
	if _, _, arr := f.carol.do("GET", "/api/v1/lists", nil, nil); len(arr) != 0 {
		t.Errorf("stranger index = %v", arr)
	}

	// Owner adds bob.
	f.call(f.c, "POST", lpath(id, "/members"), map[string]any{"username": "nobody"}, 404)
	f.call(f.c, "POST", lpath(id, "/members"), map[string]any{"username": "alice"}, 422)
	code, _, members := f.c.do("POST", lpath(id, "/members"), map[string]any{"username": "BOB"}, nil)
	if code != 200 || len(members) != 1 || members[0].(map[string]any)["username"] != "bob" {
		t.Fatalf("add member: %d %v", code, members)
	}

	// Collaborator: sees, edits, adds items and members, cannot delete or change visibility.
	l := f.call(f.bob, "GET", lpath(id, ""), nil, 200)
	if l["is_owner"] != false || l["can_edit"] != true || l["can_delete"] != false || len(itemTitles(l)) != 1 {
		t.Errorf("collaborator view = %v", l)
	}
	if _, _, arr := f.bob.do("GET", "/api/v1/lists", nil, nil); len(arr) != 1 {
		t.Errorf("collaborator index = %v", arr)
	}
	f.call(f.bob, "PATCH", lpath(id, ""), map[string]any{"description": "bob was here"}, 200)
	f.call(f.bob, "PATCH", lpath(id, ""), map[string]any{"visibility": "public"}, 403)
	f.call(f.bob, "DELETE", lpath(id, ""), nil, 403)
	show := f.show("100", "Plain Show", catalogtest.Season{Number: 1, Episodes: catalogtest.Eps(1, 1, "2020-01-01")})
	f.call(f.bob, "POST", lpath(id, "/items"), map[string]any{"item_id": show.ID}, 200)
	f.call(f.bob, "DELETE", lpath(id, fmt.Sprintf("/items/%d", movie.ID)), nil, 204)

	// Collaborator can leave; then it is a stranger again.
	code, _, _ = f.bob.do("DELETE", lpath(id, "/members"), map[string]any{"username": "bob"}, nil)
	if code != 200 {
		t.Fatalf("leave: %d", code)
	}
	f.call(f.bob, "GET", lpath(id, ""), nil, 404)
	f.call(f.c, "DELETE", lpath(id, "/members"), map[string]any{"username": "bob"}, 404)

	f.call(f.carol, "DELETE", lpath(id, ""), nil, 404)
	f.call(f.c, "DELETE", lpath(id, ""), nil, 204)
}

func TestListsPublic(t *testing.T) {
	f := newListFx(t)
	id := f.create("Open", map[string]any{"visibility": "public"})
	movie := f.movie("300", "Plain Movie")
	f.call(f.c, "POST", lpath(id, "/items"), map[string]any{"item_id": movie.ID}, 200)

	l := f.call(f.carol, "GET", lpath(id, ""), nil, 200)
	if l["can_edit"] != false || l["is_owner"] != false || len(itemTitles(l)) != 1 {
		t.Errorf("public view = %v", l)
	}
	// Read-only for non-members; not in their own index.
	f.call(f.carol, "POST", lpath(id, "/items"), map[string]any{"item_id": movie.ID}, 403)
	f.call(f.carol, "PATCH", lpath(id, ""), map[string]any{"name": "mine"}, 403)
	f.call(f.carol, "POST", lpath(id, "/members"), map[string]any{"username": "carol"}, 403)
	f.call(f.carol, "DELETE", lpath(id, ""), nil, 403)
	if _, _, arr := f.carol.do("GET", "/api/v1/lists", nil, nil); len(arr) != 0 {
		t.Errorf("public list in stranger index: %v", arr)
	}
	// Back to private hides it again.
	f.call(f.c, "PATCH", lpath(id, ""), map[string]any{"visibility": "private"}, 200)
	f.call(f.carol, "GET", lpath(id, ""), nil, 404)
}

func TestListsItems(t *testing.T) {
	f := newListFx(t)
	f.env.TMDB.AddMovie("300", "Plain Movie", "2020-01-01")
	f.env.TMDB.AddMovie("301", "Another Movie", "2021-01-01")
	id := f.create("L", nil)

	// By external ids (creates the item), numeric or string media_id, idempotent.
	a := f.call(f.c, "POST", lpath(id, "/items"), map[string]any{"media_type": "movie", "source": "tmdb", "media_id": 300}, 200)
	if a["item"].(map[string]any)["title"] != "Plain Movie" || a["entry"] != nil {
		t.Errorf("added = %v", a)
	}
	f.call(f.c, "POST", lpath(id, "/items"), map[string]any{"media_type": "movie", "source": "tmdb", "media_id": "300"}, 200)
	f.call(f.c, "POST", lpath(id, "/items"), map[string]any{"media_type": "movie", "source": "tmdb", "media_id": "301"}, 200)
	f.call(f.c, "POST", lpath(id, "/items"), map[string]any{"media_type": "movie", "source": "tmdb", "media_id": "999"}, 404)
	f.call(f.c, "POST", lpath(id, "/items"), map[string]any{"media_type": "nope", "source": "tmdb", "media_id": "1"}, 422)
	f.call(f.c, "POST", lpath(id, "/items"), map[string]any{"media_type": "movie", "source": "tmdb"}, 422)
	f.call(f.c, "POST", lpath(id, "/items"), map[string]any{"item_id": 99999}, 404)
	l := f.call(f.c, "GET", lpath(id, ""), nil, 200)
	if num(l["items_count"]) != 2 {
		t.Fatalf("items = %v", l["items"])
	}

	// The viewer's entry status shows on items they track, and filters.
	items := l["items"].([]any)
	second := db.Item{ID: int64(num(items[0].(map[string]any)["item"].(map[string]any)["id"]))} // newest = 301
	if _, err := f.lib.Track(context.Background(), f.uid, second.ID, library.Planning, library.SourceManual, overviewT0); err != nil {
		t.Fatal(err)
	}
	l = f.call(f.c, "GET", lpath(id, ""), nil, 200)
	tracked := 0
	for _, v := range l["items"].([]any) {
		if e, ok := v.(map[string]any)["entry"].(map[string]any); ok {
			tracked++
			if e["status"] != "Planning" {
				t.Errorf("entry = %v", e)
			}
		}
	}
	if tracked != 1 {
		t.Errorf("tracked entries = %d", tracked)
	}
	if got := f.call(f.c, "GET", lpath(id, "?status=Planning"), nil, 200); len(itemTitles(got)) != 1 || num(got["items_count"]) != 1 {
		t.Errorf("status filter = %v", itemTitles(got))
	}
	if got := f.call(f.c, "GET", lpath(id, "?status=All&q=plain"), nil, 200); len(itemTitles(got)) != 1 {
		t.Errorf("q filter = %v", itemTitles(got))
	}
	if got := f.call(f.c, "GET", lpath(id, "?type=manga"), nil, 200); len(itemTitles(got)) != 0 {
		t.Errorf("type filter = %v", itemTitles(got))
	}
	f.call(f.c, "GET", lpath(id, "?status=Nope"), nil, 422)
	// has_item for the "add to list" menu.
	if _, _, arr := f.c.do("GET", fmt.Sprintf("/api/v1/lists?item_id=%d", second.ID), nil, nil); arr[0].(map[string]any)["has_item"] != true {
		t.Errorf("has_item = %v", arr)
	}

	// Remove by body and by path; missing is 404.
	f.call(f.c, "DELETE", lpath(id, "/items"), map[string]any{"media_type": "movie", "source": "tmdb", "media_id": "300"}, 204)
	f.call(f.c, "DELETE", lpath(id, "/items"), map[string]any{"media_type": "movie", "source": "tmdb", "media_id": "300"}, 404)
	f.call(f.c, "DELETE", lpath(id, fmt.Sprintf("/items/%d", second.ID)), nil, 204)
	if l := f.call(f.c, "GET", lpath(id, ""), nil, 200); num(l["items_count"]) != 0 {
		t.Errorf("after removals = %v", l["items"])
	}
}

func TestListsOrdering(t *testing.T) {
	f := newListFx(t)
	var ids []int64
	for i, title := range []string{"Charlie", "alpha", "Bravo"} {
		it := f.movie(fmt.Sprint(300+i), title)
		ids = append(ids, it.ID)
		// Distinct added_at seconds so date_added order is deterministic.
		f.env.Advance(1_000_000_000)
	}
	id := f.create("Ordered", nil)
	f.s.Catalog.Now = f.env.Now
	for _, itemID := range ids {
		f.call(f.c, "POST", lpath(id, "/items"), map[string]any{"item_id": itemID}, 200)
		f.env.Advance(1_000_000_000)
	}
	order := func(q string) []string { return itemTitles(f.call(f.c, "GET", lpath(id, q), nil, 200)) }
	eq := func(got []string, want ...string) {
		t.Helper()
		if fmt.Sprint(got) != fmt.Sprint(want) {
			t.Errorf("order = %v, want %v", got, want)
		}
	}
	eq(order(""), "Bravo", "alpha", "Charlie") // date_added, newest first
	eq(order("?sort=title"), "alpha", "Bravo", "Charlie")
	eq(order("?sort=position"), "Charlie", "alpha", "Bravo") // append order

	f.call(f.c, "PUT", lpath(id, "/order"), map[string]any{"item_ids": []int64{ids[2], ids[0]}}, 200)
	eq(order("?sort=position"), "Bravo", "Charlie", "alpha")
	f.call(f.c, "PUT", lpath(id, "/order"), map[string]any{"item_ids": []int64{12345}}, 422)

	// A new item goes to the end of the manual order; pagination slices the sorted list.
	it := f.movie("310", "Delta")
	f.call(f.c, "POST", lpath(id, "/items"), map[string]any{"item_id": it.ID}, 200)
	eq(order("?sort=position"), "Bravo", "Charlie", "alpha", "Delta")
	eq(order("?sort=position&page=2&page_size=3"), "Delta")
	if got := f.call(f.c, "GET", lpath(id, "?page_size=2"), nil, 200); num(got["items_count"]) != 4 || len(itemTitles(got)) != 2 {
		t.Errorf("pagination = %v", got)
	}
	f.call(f.carol, "PUT", lpath(id, "/order"), map[string]any{"item_ids": []int64{}}, 404)
}
