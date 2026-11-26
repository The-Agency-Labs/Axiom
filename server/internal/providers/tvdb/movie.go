package tvdb

import (
	"context"
	"net/url"
	"strconv"
)

const movieCharactersPref = "tvdb_movie_characters_"

// MovieCharacters returns the characters TVDB lists for the film with this
// IMDb id, which carry the character pictures TMDB lacks. None when TVDB
// doesn't have the film.
func (c *Client) MovieCharacters(ctx context.Context, imdbID string) ([]Character, error) {
	key := movieCharactersPref + imdbID
	var cached struct {
		Found      bool
		Characters []Character
	}
	if c.cacheGet(ctx, key, &cached) && cached.Found {
		return cached.Characters, nil
	}
	var search struct {
		Data []map[string]any `json:"data"`
	}
	if _, err := c.get(ctx, "/search/remoteid/"+url.PathEscape(imdbID), &search); err != nil {
		return nil, err
	}
	for _, r := range search.Data {
		m := fmap(r, "movie")
		if m == nil {
			continue
		}
		var resp struct {
			Data map[string]any `json:"data"`
		}
		if _, err := c.get(ctx, "/movies/"+strconv.Itoa(fint(m, "id"))+"/extended", &resp); err != nil {
			return nil, err
		}
		cached.Characters = characters(resp.Data)
		break
	}
	cached.Found = true
	c.cacheSet(ctx, key, cached)
	return cached.Characters, nil
}
