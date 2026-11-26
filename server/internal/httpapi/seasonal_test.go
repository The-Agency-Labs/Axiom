package httpapi

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestSeasonWindow(t *testing.T) {
	now := time.Date(2026, 11, 26, 0, 0, 0, 0, time.UTC) // Fall 2026
	w := seasonWindow(now, 0, 0)
	if w[0].Label != "Fall 2023" || w[len(w)-1].Label != "Fall 2029" || len(w) != 25 {
		t.Fatalf("window %s .. %s (%d)", w[0].Label, w[len(w)-1].Label, len(w))
	}
	// Announced further out: extended. Announced inside the window: unchanged.
	if w := seasonWindow(now, 2031, 1); w[len(w)-1].Label != "Spring 2031" {
		t.Fatalf("extended to %s", w[len(w)-1].Label)
	}
	if w := seasonWindow(now, 2028, 2); len(w) != 25 {
		t.Fatalf("shrunk/extended: %d", len(w))
	}
	// A far-future placeholder is capped.
	if w := seasonWindow(now, 2099, 0); w[len(w)-1].Year != 2036 {
		t.Fatalf("not capped: %s", w[len(w)-1].Label)
	}
	if newSeasonRef(0, 2027).Label != "Winter 2027" {
		t.Fatal("label")
	}
}

type fakeGQL struct{ responses []string }

func (f *fakeGQL) GraphQL(_ context.Context, q string, _ map[string]any) (json.RawMessage, error) {
	r := f.responses[0]
	if len(f.responses) > 1 {
		f.responses = f.responses[1:]
	}
	return json.RawMessage(r), nil
}

func TestLastAnnounced(t *testing.T) {
	g := &fakeGQL{responses: []string{`{"Page":{"media":[
		{"season":"SPRING","seasonYear":2031,"startDate":{"year":2031,"month":4}},
		{"season":null,"seasonYear":null,"startDate":{"year":2032,"month":11}},
		{"season":null,"seasonYear":null,"startDate":{"year":2033,"month":null}}]}}`}}
	y, s, err := lastAnnounced(context.Background(), g)
	if err != nil || y != 2032 || s != 3 {
		t.Fatalf("got %d %d %v", y, s, err)
	}
}

func TestSeasonalTitles(t *testing.T) {
	g := &fakeGQL{responses: []string{
		`{"Page":{"pageInfo":{"hasNextPage":true},"media":[{"id":1,"idMal":10,"format":"TV","episodes":12,"averageScore":80,
			"title":{"userPreferred":"Romaji","english":"English","romaji":"Romaji"},"coverImage":{"large":"c"},
			"startDate":{"year":2027,"month":1,"day":5},"studios":{"nodes":[{"name":"S"}]}}]}}`,
		`{"Page":{"pageInfo":{"hasNextPage":false},"media":[{"id":2,"idMal":null,"format":"MOVIE","title":{"userPreferred":"Film"},
			"coverImage":{"large":""},"startDate":{"year":2027,"month":null,"day":null},"studios":{"nodes":[]}}]}}`}}
	got, err := seasonalTitles(context.Background(), g, "winter", 2027)
	if err != nil || len(got) != 2 {
		t.Fatalf("%v %+v", err, got)
	}
	a, b := got[0], got[1]
	if a.Source != "mal" || a.MediaID != "10" || a.Title != "English" || a.MediaType != "anime" || a.Studio != "S" || a.StartDate == nil || *a.StartDate != "2027-01-05" {
		t.Errorf("first %+v", a)
	}
	if b.Source != "anilist" || b.MediaID != "2" || b.MediaType != "anime_movie" || b.StartDate != nil || !strings.Contains(b.Title, "Film") {
		t.Errorf("second %+v", b)
	}
}
