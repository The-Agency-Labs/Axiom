package catalog

import (
	"context"
	"errors"
	"sort"
	"strconv"

	"github.com/the-agency-labs/axiom/server/internal/providers/tvdb"
)

// tvdbCrewTypes are the TVDB roles worth listing as crew.
var tvdbCrewTypes = map[string]string{
	"Creator": "Creator", "Director": "Director", "Writer": "Writer", "Showrunner": "Showrunner",
}

// tvdbCreditRows turns the characters TVDB lists for a series into credits:
// the first 40 actors by TVDB's own order, then the creators, directors and
// writers (at most 8). A person TVDB knows by a TMDB id shares one record
// with that TMDB person (same id, never the same name), so the People page of
// someone from a movie also shows their shows.
func (c *Catalog) tvdbCreditRows(ctx context.Context, item Item) ([]creditRow, error) {
	if c.Reg.P.TVDB == nil {
		return nil, errors.New("TVDB is not configured")
	}
	chars, err := c.Reg.P.TVDB.Characters(ctx, item.ExternalID)
	if err != nil {
		return nil, err
	}
	sort.SliceStable(chars, func(i, j int) bool { return chars[i].Sort < chars[j].Sort })
	var rows []creditRow
	resolve := func(ch tvdb.Character) (creditRow, error) {
		row := creditRow{
			source: SourceTVDB, extID: strconv.FormatInt(ch.PersonID, 10), name: ch.Person, image: ch.PersonImg,
		}
		ids, err := c.Reg.P.TVDB.PersonRemoteIDs(ctx, row.extID)
		if err != nil {
			return row, err
		}
		if id := ids["tmdb"]; id != "" && id != "0" {
			row.source, row.extID, row.keep = SourceTMDB, id, true
		}
		return row, nil
	}

	seenCast := map[int64]bool{}
	for _, ch := range chars {
		if len(seenCast) >= maxCast {
			break
		}
		if ch.PersonType != "Actor" || ch.PersonID == 0 || seenCast[ch.PersonID] {
			continue
		}
		seenCast[ch.PersonID] = true
		row, err := resolve(ch)
		if err != nil {
			return nil, err
		}
		row.role, row.character, row.charImage, row.order = RoleCast, ch.Character, ch.Image, len(rows)
		rows = append(rows, row)
	}
	seenCrew := map[string]bool{}
	crew := 0
	for _, ch := range chars {
		job, ok := tvdbCrewTypes[ch.PersonType]
		key := strconv.FormatInt(ch.PersonID, 10) + "|" + job
		if !ok || ch.PersonID == 0 || seenCrew[key] || crew >= maxCrew {
			continue
		}
		seenCrew[key] = true
		crew++
		row, err := resolve(ch)
		if err != nil {
			return nil, err
		}
		row.role, row.job, row.order = RoleCrew, job, len(rows)
		rows = append(rows, row)
	}
	return rows, nil
}
