package tvdb

import "testing"

// The banner is textless background art first, then English, by score;
// posters and banners (other types) are never chosen.
func TestPickBackground(t *testing.T) {
	arts := []map[string]any{
		{"type": float64(2), "image": "/poster.jpg", "score": float64(900000)},
		{"type": float64(3), "image": "/jpn.jpg", "language": "jpn", "score": float64(500000)},
		{"type": float64(3), "image": "/eng.jpg", "language": "eng", "score": float64(400000)},
		{"type": float64(3), "image": "/clean-low.jpg", "language": nil, "score": float64(10)},
		{"type": float64(3), "image": "/clean-high.jpg", "language": nil, "score": float64(100001)},
	}
	if got := pickBackground(arts); got != artworkHost+"/clean-high.jpg" {
		t.Fatalf("got %q", got)
	}
	if got := pickBackground(arts[:3]); got != artworkHost+"/eng.jpg" {
		t.Fatalf("without textless: got %q", got)
	}
	if got := pickBackground(arts[:1]); got != "" {
		t.Fatalf("no background: got %q", got)
	}
}
