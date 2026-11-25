package showconv

import (
	"fmt"
	"testing"
	"time"
)

// weekly builds n episodes of a season from number first, one a week from start.
func weekly(season, first, n int, start string, idBase int) []Ep {
	t, _ := time.Parse(time.DateOnly, start)
	out := make([]Ep, n)
	for i := range out {
		out[i] = Ep{
			Season: season, Number: first + i, Title: fmt.Sprintf("Title %d-%d", season, first+i),
			Air: t.AddDate(0, 0, 7*i).Format(time.DateOnly), ExtID: fmt.Sprint(idBase + first + i),
		}
	}
	return out
}

func TestAlignByDateSplitsOneLongSeasonIntoSeveral(t *testing.T) {
	// TMDB: one season of 24 weekly episodes. TVDB: two seasons of 12 that
	// begin the same weeks.
	src := weekly(1, 1, 24, "2020-01-06", 0)
	dst := append(weekly(1, 1, 12, "2020-01-06", 100), weekly(2, 1, 12, "2020-01-06", 200)...)
	// TVDB season 2 starts 12 weeks later, like the real thing.
	dst = append(weekly(1, 1, 12, "2020-01-06", 100), weekly(2, 1, 12, "2020-03-30", 200)...)
	al := Align(src, dst, nil, nil)
	if len(al) != 24 {
		t.Fatalf("aligned %d of 24", len(al))
	}
	for i := 1; i <= 24; i++ {
		l := al[Key{1, i}]
		want := Key{1, i}
		if i > 12 {
			want = Key{2, i - 12}
		}
		if l.To != want || l.Method != MethodDate {
			t.Fatalf("S1E%d -> %+v (%s), want %v by date", i, l.To, l.Method, want)
		}
	}
}

func TestAlignSameDayEpisodesGoInOrder(t *testing.T) {
	src := []Ep{{1, 1, "a", "2021-01-01", ""}, {1, 2, "b", "2021-01-01", ""}, {1, 3, "c", "2021-01-08", ""}}
	dst := []Ep{{1, 1, "x", "2021-01-01", "11"}, {1, 2, "y", "2021-01-01", "12"}, {1, 3, "z", "2021-01-08", "13"}}
	al := Align(src, dst, nil, nil)
	if al[Key{1, 1}].To != (Key{1, 1}) || al[Key{1, 2}].To != (Key{1, 2}) || al[Key{1, 1}].Method != MethodDateOrder || al[Key{1, 3}].Method != MethodDate {
		t.Fatalf("alignment = %+v", al)
	}
}

func TestAlignExactLinkSettlesAmbiguousDay(t *testing.T) {
	// A day with 2 episodes on one side and 3 on the other: the ids decide.
	src := []Ep{{1, 1, "", "2021-01-01", ""}, {1, 2, "", "2021-01-01", ""}}
	dst := []Ep{{1, 1, "", "2021-01-01", "11"}, {1, 2, "", "2021-01-01", "12"}, {1, 3, "", "2021-01-01", "13"}}
	asked := 0
	link := func(season, number int) string {
		asked++
		return map[int]string{1: "11", 2: "13"}[number]
	}
	al := Align(src, dst, func(Key) bool { return true }, link)
	if al[Key{1, 1}] != (Link{Key{1, 1}, MethodExact}) || al[Key{1, 2}] != (Link{Key{1, 3}, MethodExact}) {
		t.Fatalf("alignment = %+v", al)
	}
	// Links are only asked for watched episodes.
	asked = 0
	Align(src, dst, func(Key) bool { return false }, link)
	if asked != 0 {
		t.Errorf("asked %d links for unwatched episodes", asked)
	}
}

func TestAlignDayApartTitleAndOrderFill(t *testing.T) {
	src := []Ep{
		{1, 1, "One", "2021-01-01", ""},
		{1, 2, "Two", "2021-01-08", ""}, // TVDB has it a day later
		{1, 3, "Three", "", ""},         // undated: title
		{1, 4, "Episode 4", "", ""},     // undated, generic title: order
		{1, 5, "Five", "2021-01-22", ""},
	}
	dst := []Ep{
		{1, 1, "One", "2021-01-01", "1"},
		{1, 2, "Two", "2021-01-09", "2"},
		{1, 3, "Three", "2021-01-15", "3"},
		{1, 4, "Four", "2021-01-16", "4"},
		{1, 5, "Five", "2021-01-22", "5"},
	}
	al := Align(src, dst, nil, nil)
	want := map[int]string{1: MethodDate, 2: MethodDateNear, 3: MethodTitle, 4: MethodOrder, 5: MethodDate}
	for n, m := range want {
		l, ok := al[Key{1, n}]
		if !ok || l.To != (Key{1, n}) || l.Method != m {
			t.Errorf("E%d -> %+v ok=%v, want E%d by %s", n, l, ok, n, m)
		}
	}
}

func TestAlignSpecialsByDateAndTitle(t *testing.T) {
	src := []Ep{{0, 1, "Recap", "2021-02-01", ""}, {0, 2, "Behind the scenes", "", ""}, {1, 1, "Pilot", "2021-01-01", ""}}
	dst := []Ep{{0, 1, "Behind the Scenes!", "2021-03-03", "s1"}, {0, 2, "Recap", "2021-02-01", "s2"}, {1, 1, "Pilot", "2021-01-01", "e1"}}
	al := Align(src, dst, nil, nil)
	if al[Key{0, 1}].To != (Key{0, 2}) || al[Key{0, 2}].To != (Key{0, 1}) || al[Key{0, 2}].Method != MethodTitle {
		t.Fatalf("specials = %+v", al)
	}
	// A regular episode is never matched with a special.
	if al[Key{1, 1}].To != (Key{1, 1}) {
		t.Errorf("regular = %+v", al[Key{1, 1}])
	}
}

func TestOrderFillRefusesFarApartDates(t *testing.T) {
	src := []Ep{{1, 1, "", "2021-01-01", ""}, {1, 2, "", "2005-05-05", ""}}
	dst := []Ep{{1, 1, "", "2021-01-01", "1"}, {1, 2, "", "2021-01-08", "2"}}
	al := Align(src, dst, nil, nil)
	if _, ok := al[Key{1, 2}]; ok {
		t.Errorf("an episode dated 16 years off was placed: %+v", al[Key{1, 2}])
	}
}

func TestClosestFallbacks(t *testing.T) {
	dst := []Ep{{1, 1, "", "2021-01-01", ""}, {1, 2, "", "2021-01-08", ""}, {2, 1, "", "2021-06-01", ""}, {0, 1, "", "2021-02-02", ""}}
	if k, how, ok := closest(1, 40, "2021-06-03", dst); !ok || k != (Key{2, 1}) || how != "nearest air date" {
		t.Errorf("by date: %v %s %v", k, how, ok)
	}
	if k, how, _ := closest(1, 2, "", dst); k != (Key{1, 2}) || how != "same number" {
		t.Errorf("same number: %v %s", k, how)
	}
	if k, how, _ := closest(1, 40, "", dst); k != (Key{1, 2}) || how != "nearest number" {
		t.Errorf("nearest number: %v %s", k, how)
	}
	if k, _, _ := closest(0, 9, "2021-02-03", dst); k != (Key{0, 1}) {
		t.Errorf("special: %v", k)
	}
	if _, _, ok := closest(1, 1, "", nil); ok {
		t.Error("nothing to place on")
	}
}
