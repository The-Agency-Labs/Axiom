package catalog

import "testing"

func TestFillCharacterArt(t *testing.T) {
	rows := []creditRow{
		{role: RoleCast, name: "A", character: "Tanjiro Kamado (voice)"},
		{role: RoleCast, name: "B", character: "Narrator"},
		{role: RoleCast, name: "C", character: "Kept", charImage: "own.png"},
		{role: RoleCast, name: "Natsuki Hanae", character: "Nezuko Kamado (voice)"},
		{role: RoleVoice, name: "A", character: "Kamado, Tanjiro", charImage: "tanjiro.png"},
		{role: RoleVoice, name: "C", character: "Kept", charImage: "other.png"},
		{role: RoleVoice, name: "Hanae, Natsuki", character: "Nezukko Kamado", charImage: "nezuko.png"},
		{role: RoleVoice, name: "B", character: "Someone Else", charImage: "else.png"},
	}
	fillCharacterArt(rows)
	for i, want := range []string{"tanjiro.png", "", "own.png", "nezuko.png"} {
		if rows[i].charImage != want {
			t.Errorf("row %d: image %q, want %q", i, rows[i].charImage, want)
		}
	}
}
