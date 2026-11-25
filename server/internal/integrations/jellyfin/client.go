package jellyfin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// DefaultPageSize is how many items one /Items request asks for.
const DefaultPageSize = 500

// Client is a minimal read-only Jellyfin API client using an API key.
type Client struct {
	URL  string
	Key  string
	HTTP *http.Client
	// PageSize overrides DefaultPageSize.
	PageSize int
}

// NewClient returns a client for the server at baseURL.
func NewClient(baseURL, apiKey string, httpClient *http.Client) (*Client, error) {
	if baseURL == "" || apiKey == "" {
		return nil, errors.New("JELLYFIN_URL and JELLYFIN_API_KEY must be set")
	}
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 30 * time.Second}
	}
	return &Client{URL: strings.TrimRight(baseURL, "/"), Key: apiKey, HTTP: httpClient}, nil
}

// Item is the part of a Jellyfin item the import reads.
type Item struct {
	ID                string            `json:"Id"`
	Type              string            `json:"Type"`
	Name              string            `json:"Name"`
	SeriesName        string            `json:"SeriesName"`
	SeriesID          string            `json:"SeriesId"`
	ProductionYear    *int              `json:"ProductionYear"`
	ParentIndexNumber *int              `json:"ParentIndexNumber"`
	IndexNumber       *int              `json:"IndexNumber"`
	ProviderIDs       map[string]string `json:"ProviderIds"`
	UserData          struct {
		Played         bool   `json:"Played"`
		LastPlayedDate string `json:"LastPlayedDate"`
	} `json:"UserData"`
}

func (c *Client) get(ctx context.Context, path string, q url.Values, dst any) error {
	u := c.URL + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", fmt.Sprintf("MediaBrowser Token=%q", c.Key))
	req.Header.Set("Accept", "application/json")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("jellyfin request failed: %s: %w", path, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("jellyfin request failed: %s: %s", path, resp.Status)
	}
	return json.NewDecoder(resp.Body).Decode(dst)
}

// UserID looks up the Jellyfin user id for a username (case-insensitive).
func (c *Client) UserID(ctx context.Context, username string) (string, error) {
	var users []struct{ ID, Name string }
	if err := c.get(ctx, "/Users", nil, &users); err != nil {
		return "", err
	}
	for _, u := range users {
		if strings.EqualFold(u.Name, username) {
			return u.ID, nil
		}
	}
	return "", fmt.Errorf("no Jellyfin user named %q", username)
}

// Items returns every item of the given types visible to the user, paged.
// filters are extra query parameters (e.g. Filters=IsPlayed).
func (c *Client) Items(ctx context.Context, userID, types string, filters url.Values) ([]Item, error) {
	var all []Item
	pageSize := c.PageSize
	if pageSize <= 0 {
		pageSize = DefaultPageSize
	}
	for start := 0; ; start += pageSize {
		q := url.Values{
			"userId":           {userID},
			"Recursive":        {"true"},
			"IncludeItemTypes": {types},
			"Fields":           {"ProviderIds,ProductionYear"},
			"EnableImages":     {"false"},
			"StartIndex":       {strconv.Itoa(start)},
			"Limit":            {strconv.Itoa(pageSize)},
		}
		for k, v := range filters {
			q[k] = v
		}
		var page struct {
			Items            []Item `json:"Items"`
			TotalRecordCount int    `json:"TotalRecordCount"`
		}
		if err := c.get(ctx, "/Items", q, &page); err != nil {
			return nil, err
		}
		all = append(all, page.Items...)
		if len(page.Items) == 0 || start+pageSize >= page.TotalRecordCount {
			return all, nil
		}
	}
}
