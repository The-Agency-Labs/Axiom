package library

import "testing"

// The owner's Bleach sequence: unmark two seasons, mark the show Completed
// (re-marking everything), unmark three seasons, all within a minute. The
// feed shows one row with the net change; the status flip-flop cancels out.
func TestMergeBurstsShowsNetChange(t *testing.T) {
	ch := func(at string, id int64, cs ...Change) ActivityEntry {
		return ActivityEntry{At: at, Kind: "change", EntryID: 14, Changes: cs, id: id}
	}
	p := func(old, new string) Change { return Change{Field: "progress", Old: old, New: new} }
	st := func(old, new string) Change { return Change{Field: "status", Old: old, New: new} }
	rows := []ActivityEntry{ // newest first
		ch("2026-11-26T00:37:15Z", 8, p("375", "353")),
		ch("2026-11-26T00:37:13Z", 7, p("396", "375")),
		ch("2026-11-26T00:37:11Z", 5, st("Completed", "In progress"), p("416", "396")),
		ch("2026-11-26T00:37:09Z", 3, st("In progress", "Completed"), p("375", "416")),
		ch("2026-11-26T00:37:06Z", 2, p("396", "375")),
		ch("2026-11-26T00:37:04Z", 1, p("416", "396")),
		{At: "2026-11-26T00:20:00Z", Kind: "watch", EntryID: 14, id: 9}, // a play ends the burst
		ch("2026-11-26T00:10:00Z", 0, p("415", "416")),
	}
	got := mergeBursts(rows)
	if len(got) != 3 {
		t.Fatalf("rows = %d, want 3: %+v", len(got), got)
	}
	if c := got[0].Changes; len(c) != 1 || c[0] != p("416", "353") || got[0].id != 1 || got[0].At != "2026-11-26T00:37:15Z" {
		t.Fatalf("merged = %+v (id %d)", c, got[0].id)
	}
	if got[2].Changes[0] != p("415", "416") {
		t.Fatalf("row after the play was merged: %+v", got[2])
	}
}

// Marking a season watched is one row: "episodes 20–24", not five rows.
func TestMergeBurstsFoldsSeasonPlays(t *testing.T) {
	play := func(at string, id int64, ep int) ActivityEntry {
		s := 1
		return ActivityEntry{At: at, Kind: "watch", EntryID: 3, Season: &s, Episode: &ep, id: id}
	}
	got := mergeBursts([]ActivityEntry{
		play("2026-11-26T00:02:05Z", 5, 24), play("2026-11-26T00:02:04Z", 4, 23), play("2026-11-26T00:02:03Z", 3, 22),
		play("2026-11-26T00:02:02Z", 2, 21), play("2026-11-26T00:02:01Z", 1, 20),
	})
	if len(got) != 1 || *got[0].FirstEpisode != 20 || *got[0].Episode != 24 || got[0].Plays != 5 || got[0].id != 1 {
		t.Fatalf("got %+v", got)
	}
}
