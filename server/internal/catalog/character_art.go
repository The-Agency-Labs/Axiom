package catalog

import (
	"context"
	"log/slog"
	"sort"
	"strconv"
	"strings"
	"unicode"
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

// characterKey normalises a character name so "Luffy, Monkey D." and
// "Monkey D. Luffy" (or different case and punctuation) match: lower-case
// words, sorted.
func characterKey(name string) string {
	words := strings.FieldsFunc(strings.ToLower(name), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
	sort.Strings(words)
	return strings.Join(words, " ")
}
