package openlibrary

import (
	"context"
	"strings"
)

// EditionSeries returns the free-text series strings of an edition ("Red
// Rising Saga #2"), in the order Open Library lists them. Editions without
// any give an empty list.
func (c *Client) EditionSeries(ctx context.Context, mediaID string) ([]string, error) {
	var out []string
	err := c.base.Cached(ctx, "openlibrary_edition_series_"+mediaID, cacheTTL, &out, func() (any, error) {
		var book struct {
			Series []string `json:"series"`
		}
		if err := c.get(ctx, c.Root+"/books/"+mediaID+".json", &book); err != nil {
			return nil, err
		}
		res := []string{}
		for _, s := range book.Series {
			if s = strings.TrimSpace(s); s != "" {
				res = append(res, s)
			}
		}
		return res, nil
	})
	return out, err
}
