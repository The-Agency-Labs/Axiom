// Package providers holds the shared HTTP client for metadata providers
// (TMDB, AniList, …): per-host rate limits, retries, caching, and the error
// type. Each provider lives in its own subpackage.
package providers

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/the-agency-labs/axiom/server/internal/cache"
)

// Error is a provider failure. Its message matches the Django app's
// ProviderAPIError so clients see the same text.
type Error struct {
	Provider string // label, e.g. "The Movie Database"
	Status   int    // HTTP status, 0 for network errors
	Details  string
	Err      error
}

func (e *Error) Error() string {
	switch {
	case e.Status == 0 && e.Details != "":
		return fmt.Sprintf("There was an error contacting the %s API (network error): %s. Check the logs for more details.", e.Provider, e.Details)
	case e.Status == 0:
		return fmt.Sprintf("There was an error contacting the %s API (network error). Check the logs for more details.", e.Provider)
	case e.Details != "":
		return fmt.Sprintf("There was an error contacting the %s API (HTTP %d): %s. Check the logs for more details.", e.Provider, e.Status, e.Details)
	default:
		return fmt.Sprintf("There was an error contacting the %s API (HTTP %d). Check the logs for more details.", e.Provider, e.Status)
	}
}

func (e *Error) Unwrap() error { return e.Err }

// NotFound reports whether err is a provider 404.
func NotFound(err error) bool {
	var pe *Error
	return errors.As(err, &pe) && pe.Status == http.StatusNotFound
}

// NotFoundError builds the 404 error for a missing item ("Game with ID 12 not found").
func NotFoundError(provider, kind, id string) *Error {
	if kind == "" {
		kind = "item"
	}
	return &Error{Provider: provider, Status: http.StatusNotFound,
		Details: fmt.Sprintf("%s%s with ID %s not found", strings.ToUpper(kind[:1]), kind[1:], id)}
}

// Client is the shared HTTP client. Safe for concurrent use.
type Client struct {
	HTTP  *http.Client
	Cache *cache.Cache

	mu       sync.Mutex
	limiters map[string]*limiter
}

// NewClient returns a client with a 30 s timeout and the per-host limits
// the Django app used.
func NewClient(c *cache.Cache) *Client {
	return &Client{
		HTTP:  &http.Client{Timeout: 30 * time.Second},
		Cache: c,
		limiters: map[string]*limiter{
			"api4.thetvdb.com":       newLimiter(10, time.Second),
			"api.myanimelist.net":    newLimiter(30, time.Minute),
			"graphql.anilist.co":     newLimiter(28, time.Minute),
			"api.igdb.com":           newLimiter(3, time.Second),
			"comicvine.gamespot.com": newLimiter(190, time.Hour),
			"openlibrary.org":        newLimiter(20, time.Minute),
			"api.hardcover.app":      newLimiter(50, time.Minute),
			"boardgamegeek.com":      newLimiter(2, time.Second),
			"":                       newLimiter(5, time.Second), // everything else
		},
	}
}

// Request describes one provider call.
type Request struct {
	Provider string // label for errors
	Method   string // default GET
	URL      string
	Header   http.Header
	Body     any // JSON-encoded when set; a string or []byte is sent as-is
}

// Do performs the request, waiting for the host's rate limit, retrying
// network errors and 429/5xx up to 3 times (honouring Retry-After), and
// returns the body of a 2xx response.
func (c *Client) Do(ctx context.Context, req Request) ([]byte, error) {
	method := req.Method
	if method == "" {
		method = http.MethodGet
	}
	var payload []byte
	rawBody := false
	switch b := req.Body.(type) {
	case nil:
	case string:
		payload, rawBody = []byte(b), true
	case []byte:
		payload, rawBody = b, true
	default:
		var err error
		if payload, err = json.Marshal(req.Body); err != nil {
			return nil, err
		}
	}
	var lastErr error
	for attempt := 0; attempt < 4; attempt++ {
		httpReq, err := http.NewRequestWithContext(ctx, method, req.URL, bytes.NewReader(payload))
		if err != nil {
			return nil, err
		}
		for k, v := range req.Header {
			httpReq.Header[k] = v
		}
		if payload != nil && !rawBody && httpReq.Header.Get("Content-Type") == "" {
			httpReq.Header.Set("Content-Type", "application/json")
		}
		if httpReq.Header.Get("User-Agent") == "" {
			httpReq.Header.Set("User-Agent", "Axiom")
		}
		if err := c.limiterFor(httpReq.URL.Host).wait(ctx); err != nil {
			return nil, err
		}
		resp, err := c.HTTP.Do(httpReq)
		if err != nil {
			lastErr = &Error{Provider: req.Provider, Err: err}
			if ctx.Err() != nil {
				return nil, lastErr
			}
			sleep(ctx, backoff(attempt, ""))
			continue
		}
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
		resp.Body.Close()
		if readErr != nil {
			lastErr = &Error{Provider: req.Provider, Err: readErr}
			continue
		}
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			return body, nil
		}
		lastErr = &Error{Provider: req.Provider, Status: resp.StatusCode, Err: fmt.Errorf("%s", truncate(body))}
		if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
			slog.WarnContext(ctx, "provider retry", "provider", req.Provider, "status", resp.StatusCode, "attempt", attempt+1)
			sleep(ctx, backoff(attempt, resp.Header.Get("Retry-After")))
			continue
		}
		slog.ErrorContext(ctx, "provider error", "provider", req.Provider, "status", resp.StatusCode, "body", truncate(body))
		return nil, lastErr
	}
	return nil, lastErr
}

// JSON performs the request and decodes a JSON response into dst.
func (c *Client) JSON(ctx context.Context, req Request, dst any) error {
	body, err := c.Do(ctx, req)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(body, dst); err != nil {
		return &Error{Provider: req.Provider, Status: http.StatusOK, Details: "invalid JSON response", Err: err}
	}
	return nil
}

// XML performs the request and decodes an XML response into dst.
func (c *Client) XML(ctx context.Context, req Request, dst any) error {
	body, err := c.Do(ctx, req)
	if err != nil {
		return err
	}
	if err := xml.Unmarshal(body, dst); err != nil {
		return &Error{Provider: req.Provider, Status: http.StatusOK, Details: "invalid XML response", Err: err}
	}
	return nil
}

// Cached returns the cached value for key, or runs fetch and caches its
// result for ttl. Concurrent calls for one key share a single fetch.
func (c *Client) Cached(ctx context.Context, key string, ttl time.Duration, dst any, fetch func() (any, error)) error {
	if c.Cache == nil {
		v, err := fetch()
		if err != nil {
			return err
		}
		data, err := json.Marshal(v)
		if err != nil {
			return err
		}
		return json.Unmarshal(data, dst)
	}
	return c.Cache.GetOrFetch(ctx, key, ttl, dst, fetch)
}

func (c *Client) limiterFor(host string) *limiter {
	c.mu.Lock()
	defer c.mu.Unlock()
	if l, ok := c.limiters[host]; ok {
		return l
	}
	return c.limiters[""]
}

func backoff(attempt int, retryAfter string) time.Duration {
	if secs, err := strconv.Atoi(strings.TrimSpace(retryAfter)); err == nil && secs > 0 {
		return min(time.Duration(secs)*time.Second, 2*time.Minute)
	}
	return time.Duration(1<<attempt) * time.Second
}

func sleep(ctx context.Context, d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-t.C:
	}
}

func truncate(body []byte) string {
	const max = 500
	if len(body) > max {
		return string(body[:max]) + "…"
	}
	return string(body)
}

// limiter allows n requests per period (a token bucket refilled evenly).
type limiter struct {
	mu       sync.Mutex
	interval time.Duration
	next     time.Time
}

func newLimiter(n int, per time.Duration) *limiter {
	return &limiter{interval: per / time.Duration(n)}
}

func (l *limiter) wait(ctx context.Context) error {
	l.mu.Lock()
	now := time.Now()
	at := l.next
	if at.Before(now) {
		at = now
	}
	l.next = at.Add(l.interval)
	l.mu.Unlock()
	if d := time.Until(at); d > 0 {
		t := time.NewTimer(d)
		defer t.Stop()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
		}
	}
	return nil
}
