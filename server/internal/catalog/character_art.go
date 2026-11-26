package catalog

import (
	"context"
	"encoding/json"
	"log/slog"
	"slices"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"github.com/the-agency-labs/axiom/server/internal/providers/tvdb"
)

// tvdbCharacterArt maps a normalised character name to the picture TheTVDB
// has for that character of the anime. AniList (the voice-actor source) only
// offers small thumbnails and sometimes none, so its characters are matched
// by name against the series' TVDB characters, found through the anime
// mapping's TVDB id. Anything missing (no TVDB key, no mapping, an error)
// just yields an empty map: the AniList picture is used instead.
func (c *Catalog) tvdbCharacterArt(ctx context.Context, item Item) map[string]string {
	if c.Reg.P.TVDB == nil || c.Reg.Mappings == nil {
		return nil
	}
	seriesID := int64(0)
	switch item.Source {
	case SourceTMDB:
		if m := c.Reg.pickMapping(ctx, item.Kind, item.ExternalID); m != nil && m.TvdbID.Valid {
			seriesID = m.TvdbID.Int64
		}
	case SourceMAL, SourceAniList:
		m, err := c.Reg.Mappings.Lookup(ctx, item.Source, item.ExternalID)
		if err == nil && m != nil && m.TvdbID.Valid {
			seriesID = m.TvdbID.Int64
		}
	}
	if seriesID == 0 {
		return nil
	}
	chars, err := c.Reg.P.TVDB.Characters(ctx, strconv.FormatInt(seriesID, 10))
	if err != nil {
		slog.WarnContext(ctx, "catalog: TVDB character art unavailable", "item", item.ID, "tvdb", seriesID, "err", err)
		return nil
	}
	art := map[string]string{}
	for _, ch := range chars {
		if ch.Image == "" || ch.Character == "" {
			continue
		}
		if key := characterKey(ch.Character); key != "" && art[key] == "" {
			art[key] = ch.Image
		}
	}
	return art
}

// fillCharacterArt gives cast rows without a character picture (TMDB has
// none) the one from the voice rows, so an anime film's TMDB cast shows its
// characters too. A character is matched by name or, since romanisations
// differ ("Tanjiro", "Tanjirou"), through the actor: the same person's voice
// row whose character shares a word of the name.
func fillCharacterArt(rows []creditRow) {
	byCharacter := map[string]string{}
	byActor := map[string][]creditRow{}
	for _, r := range rows {
		if r.role != RoleVoice || r.charImage == "" {
			continue
		}
		if key := characterKey(r.character); key != "" && byCharacter[key] == "" {
			byCharacter[key] = r.charImage
		}
		if key := characterKey(r.name); key != "" {
			byActor[key] = append(byActor[key], r)
		}
	}
	for i, r := range rows {
		if r.role != RoleCast || r.charImage != "" {
			continue
		}
		name := strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(r.character), "(voice)"))
		if name == "" {
			continue
		}
		if img := byCharacter[characterKey(name)]; img != "" {
			rows[i].charImage = img
			continue
		}
		voiced := byActor[characterKey(r.name)]
		for _, v := range voiced {
			if sharesWord(name, v.character) {
				rows[i].charImage = v.charImage
				break
			}
		}
	}
}

const aniListFilmQuery = `query ($search: String) {
  Page(perPage: 5) { media(search: $search, type: ANIME, format: MOVIE) { id startDate { year } } }
}`

// findAniListFilm is the AniList id of an anime film, found by its title and
// released within a year of it; "" when nothing fits.
func (c *Catalog) findAniListFilm(ctx context.Context, item Item) (string, error) {
	if !item.Year.Valid {
		return "", nil
	}
	raw, err := c.Reg.P.AniList.GraphQL(ctx, aniListFilmQuery, map[string]any{"search": item.Title})
	if err != nil {
		return "", err
	}
	var resp struct {
		Page struct {
			Media []struct {
				ID        int64 `json:"id"`
				StartDate struct {
					Year int64 `json:"year"`
				} `json:"startDate"`
			} `json:"media"`
		} `json:"Page"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return "", err
	}
	for _, m := range resp.Page.Media {
		if d := m.StartDate.Year - item.Year.Int64; d >= -1 && d <= 1 {
			return strconv.FormatInt(m.ID, 10), nil
		}
	}
	return "", nil
}

// filmCharacters is the part of the TVDB client that knows films.
type filmCharacters interface {
	MovieCharacters(ctx context.Context, imdbID string) ([]tvdb.Character, error)
}

// filmCharacterArt gives a film's cast rows the character pictures TheTVDB has
// (TMDB has none), found by the film's IMDb id and matched on the actor, or
// else the character's name. Anything missing just leaves the row as it was.
func (c *Catalog) filmCharacterArt(ctx context.Context, item Item, rows []creditRow) {
	films, ok := c.Reg.P.TVDB.(filmCharacters)
	if !ok {
		return
	}
	ids, err := c.Q.ListItemIDs(ctx, item.ID)
	if err != nil {
		slog.WarnContext(ctx, "catalog: film character art", "item", item.ID, "err", err)
		return
	}
	for _, id := range ids {
		if id.Source != "imdb" {
			continue
		}
		chars, err := films.MovieCharacters(ctx, id.ExternalID)
		if err != nil {
			slog.WarnContext(ctx, "catalog: film character art unavailable", "item", item.ID, "imdb", id.ExternalID, "err", err)
			return
		}
		known := make([]creditRow, 0, len(chars))
		for _, ch := range chars {
			if ch.Image != "" && ch.Image != ch.PersonImg { // a headshot isn't the character
				known = append(known, creditRow{role: RoleVoice, name: ch.Person, character: ch.Character, charImage: ch.Image})
			}
		}
		// The same matching as for voice rows, with TVDB's list as the source.
		all := append(slices.Clone(rows), known...)
		fillCharacterArt(all)
		copy(rows, all[:len(rows)])
		return
	}
}

// sharesWord reports whether two names have a whole word in common.
func sharesWord(a, b string) bool {
	words := strings.Fields(characterKey(b))
	for _, w := range strings.Fields(characterKey(a)) {
		if slices.Contains(words, w) {
			return true
		}
	}
	return false
}

var longVowels = strings.NewReplacer("ou", "o", "oo", "o", "uu", "u")

// characterKey normalises a character name so "Luffy, Monkey D." and
// "Monkey D. Luffy" (or different case and punctuation) match: lower-case
// words, sorted, with long vowels folded ("Douma" and "Doma", "Satou" and
// "Sato" are one name romanised two ways).
func characterKey(name string) string {
	words := strings.FieldsFunc(strings.ToLower(name), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
	for i, w := range words {
		words[i] = longVowels.Replace(w)
	}
	sort.Strings(words)
	return strings.Join(words, " ")
}
