package catalog

import (
	"context"
	"log/slog"
	"slices"
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

// fillCharacterArt gives cast rows without a character picture (TMDB has
// none) the one from the voice rows, so an anime film's TMDB cast shows its
// characters too. A character is matched by name or, since romanisations
// differ ("Tanjiro", "Tanjirou"), through the actor: the same person's voice
// row, when it is their only character here or shares a word of the name.
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

// characterKey normalises a character name so "Luffy, Monkey D." and
// "Monkey D. Luffy" (or different case and punctuation) match: lower-case
// words, sorted.
func characterKey(name string) string {
	words := strings.FieldsFunc(strings.ToLower(name), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
	sort.Strings(words)
	return strings.Join(words, " ")
}
