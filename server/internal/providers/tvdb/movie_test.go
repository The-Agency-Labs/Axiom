package tvdb

import "testing"

func TestCharactersReadsAFilmRecord(t *testing.T) {
	got := characters(map[string]any{"characters": []any{map[string]any{
		"name": "Thanos", "personName": "Josh Brolin", "peopleType": "Actor",
		"image": "/banners/person/1/movie-8.jpg", "personImgURL": "/banners/person/1/primary.jpg"}}})
	if len(got) != 1 || got[0].Character != "Thanos" || got[0].Person != "Josh Brolin" || got[0].Image == got[0].PersonImg || got[0].Image == "" {
		t.Fatalf("characters = %+v", got)
	}
}
