package igdb

import "testing"

// Logos and small art are never the banner; a wide screenshot stands in.
func TestArtworkURLSkipsLogos(t *testing.T) {
	g := map[string]any{
		"artworks":    []any{map[string]any{"image_id": "logo", "width": float64(600), "height": float64(300)}},
		"screenshots": []any{map[string]any{"image_id": "shot", "width": float64(1920), "height": float64(1080)}},
	}
	if got := artworkURL(g); got != "https://images.igdb.com/igdb/image/upload/t_1080p/shot.jpg" {
		t.Fatalf("got %v", got)
	}
	g["artworks"] = append(g["artworks"].([]any), map[string]any{"image_id": "key", "width": float64(3840), "height": float64(2160)})
	if got := artworkURL(g); got != "https://images.igdb.com/igdb/image/upload/t_1080p/key.jpg" {
		t.Fatalf("got %v", got)
	}
	if got := artworkURL(map[string]any{}); got != nil {
		t.Fatalf("got %v", got)
	}
}
