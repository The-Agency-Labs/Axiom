package providers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestDoRetriesServerErrorsThenSucceeds(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) < 3 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()
	c := NewClient(nil)
	c.limiters[""] = newLimiter(1000, time.Second)
	var out struct{ OK bool }
	if err := c.JSON(context.Background(), Request{Provider: "Test", URL: srv.URL}, &out); err != nil {
		t.Fatal(err)
	}
	if !out.OK || calls.Load() != 3 {
		t.Fatalf("ok=%v calls=%d", out.OK, calls.Load())
	}
}

func TestDoReturnsProviderErrorOn404(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	defer srv.Close()
	c := NewClient(nil)
	_, err := c.Do(context.Background(), Request{Provider: "The Movie Database", URL: srv.URL})
	if !NotFound(err) {
		t.Fatalf("want 404 provider error, got %v", err)
	}
	want := "There was an error contacting the The Movie Database API (HTTP 404). Check the logs for more details."
	if err.Error() != want {
		t.Fatalf("message %q", err.Error())
	}
}

func TestLimiterSpacesRequests(t *testing.T) {
	l := newLimiter(10, time.Second) // one per 100 ms
	start := time.Now()
	for i := 0; i < 3; i++ {
		if err := l.wait(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if elapsed := time.Since(start); elapsed < 190*time.Millisecond {
		t.Fatalf("3 calls took %v, want >= 200ms", elapsed)
	}
}
