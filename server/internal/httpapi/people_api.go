package httpapi

import (
	"database/sql"
	"errors"
	"log/slog"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/the-agency-labs/encompass/server/internal/auth"
	"github.com/the-agency-labs/encompass/server/internal/catalog"
	"github.com/the-agency-labs/encompass/server/internal/db"
	"github.com/the-agency-labs/encompass/server/internal/providers/tvdb"
)

const (
	peopleBackfillTTL   = 10 * time.Minute
	peopleStatusPlan    = "Planning"
	peopleMaxLimit      = 200
	peopleMinQueryChars = 2
)

// peopleItemJSON is api.serializers.item_dict.
type peopleItemJSON struct {
	ID            int64  `json:"id"`
	MediaID       string `json:"media_id"`
	Source        string `json:"source"`
	MediaType     string `json:"media_type"`
	Title         string `json:"title"`
	Image         string `json:"image"`
	SeasonNumber  *int   `json:"season_number"`
	EpisodeNumber *int   `json:"episode_number"`
}

func peopleItem(id int64, externalID, source, kind, title, image string) peopleItemJSON {
	return peopleItemJSON{ID: id, MediaID: externalID, Source: source, MediaType: kind, Title: title, Image: image}
}

type peopleEntryRef struct {
	MediaType string   `json:"media_type"`
	ID        int64    `json:"id"`
	Status    string   `json:"status"`
	Score     *float64 `json:"score"`
}

type peoplePersonJSON struct {
	ID         int64   `json:"id"`
	Name       string  `json:"name"`
	NativeName string  `json:"native_name"`
	Image      *string `json:"image"`
	Source     string  `json:"source"`
	URL        string  `json:"url"`
}

func peoplePersonOf(id int64, source, externalID, name, native, image string) peoplePersonJSON {
	p := peoplePersonJSON{ID: id, Name: name, NativeName: native, Source: source}
	if image != "" {
		p.Image = &image
	}
	switch source {
	case catalog.SourceTMDB:
		p.URL = "https://www.themoviedb.org/person/" + externalID
	case catalog.SourceTVDB:
		p.URL = tvdb.PersonURL(externalID)
	default:
		p.URL = "https://anilist.co/staff/" + externalID
	}
	return p
}

// peopleLabel is the stored role as the API names it (cast is "actor").
func peopleRoleOut(role string) string {
	if role == catalog.RoleCast {
		return "actor"
	}
	return role
}

// peopleRoleIn maps the API's role filter to the stored role.
func peopleRoleIn(role string) string {
	if role == "actor" {
		return catalog.RoleCast
	}
	return role
}

// peopleLanguageIn accepts ja/en as shorthand for the stored language names.
func peopleLanguageIn(lang string) string {
	switch strings.ToLower(lang) {
	case "ja":
		return "japanese"
	case "en":
		return "english"
	}
	return lang
}

// peopleLibEntry is one tracked item with its latest-run entry.
type peopleLibEntry struct {
	item  peopleItemJSON
	ref   peopleEntryRef
	kind  string
	src   string
	fetch bool
}

// peopleLibrary loads the user's cast-bearing entries: watched (not Planning)
// and planned, keyed by item id.
func (s *Server) peopleEntries(r *http.Request) (watched, planned map[int64]peopleLibEntry, err error) {
	rows, err := s.Queries.ListPeopleEntries(r.Context(), auth.User(r.Context()).ID)
	if err != nil {
		return nil, nil, err
	}
	watched, planned = map[int64]peopleLibEntry{}, map[int64]peopleLibEntry{}
	for _, row := range rows {
		e := peopleLibEntry{
			item:  peopleItem(row.ItemID, row.ExternalID, row.Source, row.Kind, row.Title, row.Image),
			ref:   peopleEntryRef{MediaType: row.Kind, ID: row.EntryID, Status: row.Status},
			kind:  row.Kind,
			src:   row.Source,
			fetch: row.Fetched,
		}
		if row.Score.Valid {
			v := row.Score.Float64
			e.ref.Score = &v
		}
		if row.Status == peopleStatusPlan {
			planned[row.ItemID] = e
		} else {
			watched[row.ItemID] = e
		}
	}
	return watched, planned, nil
}

func peopleIntParam(r *http.Request, name string, def int) (int, error) {
	raw := r.URL.Query().Get(name)
	if raw == "" {
		return def, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return 0, Errorf(http.StatusUnprocessableEntity, name+" must be an integer")
	}
	return n, nil
}

// peopleKickBackfill queues credit fetches for the user's library items at
// most every 10 minutes.
func (s *Server) peopleKickBackfill(r *http.Request, missing []int64) {
	if s.Jobs == nil || len(missing) == 0 {
		return
	}
	ctx := r.Context()
	userID := auth.User(ctx).ID
	key := "people:backfill:" + strconv.FormatInt(userID, 10)
	if s.Cache != nil {
		var held bool
		if ok, err := s.Cache.Get(ctx, key, &held); err == nil && ok {
			return
		}
		if err := s.Cache.Set(ctx, key, true, peopleBackfillTTL); err != nil {
			slog.WarnContext(ctx, "people: backfill lock", "err", err)
		}
	}
	if len(missing) > catalog.CreditsBackfillLimit {
		missing = missing[:catalog.CreditsBackfillLimit]
	}
	for _, id := range missing {
		if _, err := s.Jobs.Enqueue(ctx, catalog.JobCredits, userID, map[string]int64{"item_id": id}); err != nil {
			slog.WarnContext(ctx, "people: enqueue credits", "item", id, "err", err)
			return
		}
	}
}

type peopleAppearance struct {
	Item       peopleItemJSON `json:"item"`
	Entry      peopleEntryRef `json:"entry"`
	Characters []string       `json:"characters"`
	// CharacterImages is aligned with Characters ("" where there is no picture).
	CharacterImages []string `json:"character_images"`
	Role            string   `json:"role"`
}

type peopleResult struct {
	Person      peoplePersonJSON   `json:"person"`
	Titles      int64              `json:"titles"`
	Appearances []peopleAppearance `json:"appearances"`
}

// peopleLibrary ports library_people: people in >= min_titles watched titles.
func (s *Server) peopleLibrary(w http.ResponseWriter, r *http.Request) (any, error) {
	ctx := r.Context()
	q := r.URL.Query()
	minTitles, err := peopleIntParam(r, "min_titles", 2)
	if err != nil {
		return nil, err
	}
	limit, err := peopleIntParam(r, "limit", 60)
	if err != nil {
		return nil, err
	}
	role, language := peopleRoleIn(q.Get("role")), peopleLanguageIn(q.Get("language"))

	library, _, err := s.peopleEntries(r)
	if err != nil {
		return nil, err
	}
	var missing []int64
	for id, e := range library {
		if catalog.SupportsCredits(catalog.Item{Kind: e.kind, Source: e.src}) && !e.fetch {
			missing = append(missing, id)
		}
	}
	sort.Slice(missing, func(i, j int) bool { return missing[i] < missing[j] })
	s.peopleKickBackfill(r, missing)

	out := map[string]any{"pending": len(missing), "library_titles": len(library), "results": []peopleResult{}}
	top, err := s.Queries.ListTopPeople(ctx, db.ListTopPeopleParams{
		UserID: auth.User(ctx).ID, Role: role, Language: language,
		MinTitles: int64(max(1, minTitles)), MaxRows: int64(max(1, min(limit, peopleMaxLimit))),
	})
	if err != nil {
		return nil, err
	}
	if len(top) == 0 {
		return out, nil
	}
	ids := make([]int64, len(top))
	for i, t := range top {
		ids[i] = t.PersonID
	}
	people, err := s.Queries.ListPeopleByIDs(ctx, ids)
	if err != nil {
		return nil, err
	}
	byID := map[int64]db.Person{}
	for _, p := range people {
		byID[p.ID] = p
	}
	credits, err := s.Queries.ListCreditsForPeople(ctx, ids)
	if err != nil {
		return nil, err
	}
	type appearances struct {
		list  []*peopleAppearance
		index map[int64]*peopleAppearance
	}
	apps := map[int64]*appearances{}
	for _, c := range credits {
		entry, ok := library[c.ItemID]
		if !ok || (role != "" && c.Role != role) || (language != "" && c.Language != language) {
			continue
		}
		a := apps[c.PersonID]
		if a == nil {
			a = &appearances{index: map[int64]*peopleAppearance{}}
			apps[c.PersonID] = a
		}
		cur := a.index[c.ItemID]
		if cur == nil {
			cur = &peopleAppearance{Item: entry.item, Entry: entry.ref, Characters: []string{}, CharacterImages: []string{}, Role: peopleRoleOut(c.Role)}
			a.index[c.ItemID] = cur
			a.list = append(a.list, cur)
		}
		if peopleAddLabel(&cur.Characters, firstNonEmptyPeople(c.Character, c.Job)) {
			cur.CharacterImages = append(cur.CharacterImages, c.CharacterImage)
		}
	}
	results := make([]peopleResult, 0, len(top))
	for _, t := range top {
		p, ok := byID[t.PersonID]
		if !ok {
			continue
		}
		res := peopleResult{Person: peoplePersonOf(p.ID, p.Source, p.ExternalID, p.Name, p.NativeName, p.Image), Titles: t.Titles, Appearances: []peopleAppearance{}}
		if a := apps[t.PersonID]; a != nil {
			for _, ap := range a.list {
				res.Appearances = append(res.Appearances, *ap)
			}
		}
		results = append(results, res)
	}
	sort.SliceStable(results, func(i, j int) bool {
		if results[i].Titles != results[j].Titles {
			return results[i].Titles > results[j].Titles
		}
		return results[i].Person.Name < results[j].Person.Name
	})
	out["results"] = results
	return out, nil
}

func firstNonEmptyPeople(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func peopleAddLabel(list *[]string, label string) bool {
	if label == "" {
		return false
	}
	for _, l := range *list {
		if l == label {
			return false
		}
	}
	*list = append(*list, label)
	return true
}

type peopleTitleRole struct {
	Item            peopleItemJSON  `json:"item"`
	Entry           *peopleEntryRef `json:"entry"`
	Characters      []string        `json:"characters"`
	CharacterImages []string        `json:"character_images"`
	Role            string          `json:"role"`
	Language        string          `json:"language"`
}

// peoplePerson ports person: one person's roles in the library and elsewhere.
func (s *Server) peoplePerson(w http.ResponseWriter, r *http.Request) (any, error) {
	ctx := r.Context()
	id, err := strconv.ParseInt(r.PathValue("person_id"), 10, 64)
	if err != nil {
		return nil, Errorf(http.StatusUnprocessableEntity, "person_id must be an integer")
	}
	found, err := s.Queries.GetPerson(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, Errorf(http.StatusNotFound, "Not Found")
	}
	if err != nil {
		return nil, err
	}
	library, planned, err := s.peopleEntries(r)
	if err != nil {
		return nil, err
	}
	credits, err := s.Queries.ListPersonCredits(ctx, id)
	if err != nil {
		return nil, err
	}
	type bucket struct {
		list  []*peopleTitleRole
		index map[int64]*peopleTitleRole
	}
	inLib, elsewhere := &bucket{index: map[int64]*peopleTitleRole{}}, &bucket{index: map[int64]*peopleTitleRole{}}
	for _, c := range credits {
		b := elsewhere
		entry, ok := library[c.ItemID]
		if ok {
			b = inLib
		} else {
			entry, ok = planned[c.ItemID]
		}
		cur := b.index[c.ItemID]
		if cur == nil {
			cur = &peopleTitleRole{
				Item:       peopleItem(c.ItemID, c.ExternalID, c.Source, c.Kind, c.Title, c.Image),
				Characters: []string{}, CharacterImages: []string{}, Role: peopleRoleOut(c.Role), Language: c.Language,
			}
			if ok {
				ref := entry.ref
				cur.Entry = &ref
			}
			b.index[c.ItemID] = cur
			b.list = append(b.list, cur)
		}
		if peopleAddLabel(&cur.Characters, firstNonEmptyPeople(c.Character, c.Job)) {
			cur.CharacterImages = append(cur.CharacterImages, c.CharacterImage) // aligned with Characters ("" = none)
		}
	}
	flat := func(b *bucket) []peopleTitleRole {
		out := make([]peopleTitleRole, 0, len(b.list))
		for _, e := range b.list {
			out = append(out, *e)
		}
		return out
	}
	return map[string]any{
		"person":     peoplePersonOf(found.ID, found.Source, found.ExternalID, found.Name, found.NativeName, found.Image),
		"in_library": flat(inLib),
		"elsewhere":  flat(elsewhere),
	}, nil
}

// peopleByName ports people_by_name: catalog people whose name contains q.
func (s *Server) peopleByName(w http.ResponseWriter, r *http.Request) (any, error) {
	q, ok := r.URL.Query()["q"]
	if !ok {
		return nil, Errorf(http.StatusUnprocessableEntity, "q is required")
	}
	term := strings.TrimSpace(q[0])
	if len([]rune(term)) < peopleMinQueryChars {
		return nil, Errorf(http.StatusBadRequest, "Query too short")
	}
	esc := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(term)
	rows, err := s.Queries.SearchPeopleByName(r.Context(), "%"+esc+"%")
	if err != nil {
		return nil, err
	}
	out := make([]peoplePersonJSON, 0, len(rows))
	for _, p := range rows {
		out = append(out, peoplePersonOf(p.ID, p.Source, p.ExternalID, p.Name, p.NativeName, p.Image))
	}
	return out, nil
}

type peopleAlso struct {
	Item      peopleItemJSON `json:"item"`
	Entry     peopleEntryRef `json:"entry"`
	Character string         `json:"character"`
}

type peopleWithPerson struct {
	Person peoplePersonJSON `json:"person"`
	AlsoIn []peopleAlso     `json:"also_in"`
}

type peopleCastJSON struct {
	peopleWithPerson
	Character    string `json:"character"`
	EpisodeCount *int64 `json:"episode_count"`
}

type peopleCrewJSON struct {
	peopleWithPerson
	Job          string `json:"job"`
	EpisodeCount *int64 `json:"episode_count"`
}

type peopleVoiceJSON struct {
	peopleWithPerson
	Language string `json:"language"`
}

type peopleCharacterJSON struct {
	Character   string            `json:"character"`
	Image       *string           `json:"image"`
	Role        string            `json:"role"`
	VoiceActors []peopleVoiceJSON `json:"voice_actors"`
}

func peopleUnsupported() map[string]any {
	return map[string]any{"supported": false, "cast": []any{}, "crew": []any{}, "characters": []any{}}
}

// peopleTitleCredits ports title_credits: cast and key crew of one title,
// fetched from the provider on first view.
func (s *Server) peopleTitleCredits(w http.ResponseWriter, r *http.Request) (any, error) {
	ctx := r.Context()
	kind, source, mediaID := r.PathValue("media_type"), r.PathValue("source"), r.PathValue("media_id")
	if !catalog.SupportsCredits(catalog.Item{Kind: kind, Source: source}) {
		return peopleUnsupported(), nil
	}
	item, err := s.Catalog.EnsureItem(ctx, kind, source, mediaID)
	if errors.Is(err, catalog.ErrUnsupported) {
		return peopleUnsupported(), nil
	}
	if err != nil {
		slog.WarnContext(ctx, "people: item metadata", "kind", kind, "source", source, "id", mediaID, "err", err)
		return nil, Errorf(http.StatusBadGateway, "Metadata unavailable")
	}
	if !catalog.SupportsCredits(item) {
		return peopleUnsupported(), nil
	}
	// A failed fetch is recorded in credit_fetches; whatever is stored is served.
	_ = s.Catalog.FetchCredits(ctx, item, false)
	rows, err := s.Queries.ListItemCredits(ctx, item.ID)
	if err != nil {
		return nil, err
	}

	library, _, err := s.peopleEntries(r)
	if err != nil {
		return nil, err
	}
	others := map[int64][]peopleAlso{}
	if len(rows) > 0 {
		seenPerson := map[int64]bool{}
		var ids []int64
		for _, row := range rows {
			if !seenPerson[row.PersonID] {
				seenPerson[row.PersonID] = true
				ids = append(ids, row.PersonID)
			}
		}
		credits, err := s.Queries.ListCreditsForPeople(ctx, ids)
		if err != nil {
			return nil, err
		}
		for _, c := range credits {
			entry, ok := library[c.ItemID]
			if !ok || c.ItemID == item.ID {
				continue
			}
			others[c.PersonID] = append(others[c.PersonID], peopleAlso{
				Item: entry.item, Entry: entry.ref, Character: firstNonEmptyPeople(c.Character, c.Job),
			})
		}
	}
	withPerson := func(row db.ListItemCreditsRow) peopleWithPerson {
		seen := map[int64]bool{}
		also := []peopleAlso{}
		for _, o := range others[row.PersonID] {
			if !seen[o.Item.ID] {
				seen[o.Item.ID] = true
				also = append(also, o)
			}
		}
		return peopleWithPerson{
			Person: peoplePersonOf(row.PersonID, row.PersonSource, row.PersonExternalID, row.PersonName, row.PersonNativeName, row.PersonImage),
			AlsoIn: also,
		}
	}
	episodes := func(n sql.NullInt64) *int64 {
		if !n.Valid {
			return nil
		}
		return &n.Int64
	}

	cast, crew, characters := []peopleCastJSON{}, []peopleCrewJSON{}, []*peopleCharacterJSON{}
	charIndex := map[[2]string]*peopleCharacterJSON{}
	langs := map[string]bool{}
	for _, row := range rows {
		if row.Language != "" {
			langs[row.Language] = true
		}
		switch row.Role {
		case catalog.RoleCast:
			cast = append(cast, peopleCastJSON{withPerson(row), row.Character, episodes(row.EpisodeCount)})
		case catalog.RoleCrew:
			crew = append(crew, peopleCrewJSON{withPerson(row), row.Job, episodes(row.EpisodeCount)})
		case catalog.RoleVoice:
			key := [2]string{strconv.FormatInt(row.Ord, 10), row.Character}
			ch := charIndex[key]
			if ch == nil {
				ch = &peopleCharacterJSON{Character: row.Character, Role: strings.ToLower(row.CharacterRole), VoiceActors: []peopleVoiceJSON{}}
				if row.CharacterImage != "" {
					img := row.CharacterImage
					ch.Image = &img
				}
				charIndex[key] = ch
				characters = append(characters, ch)
			}
			ch.VoiceActors = append(ch.VoiceActors, peopleVoiceJSON{withPerson(row), row.Language})
		}
	}
	languages := make([]string, 0, len(langs))
	for l := range langs {
		languages = append(languages, l)
	}
	sort.Strings(languages)
	chars := make([]peopleCharacterJSON, len(characters))
	for i, c := range characters {
		chars[i] = *c
	}
	return map[string]any{
		"supported": true, "cast": cast, "crew": crew, "characters": chars, "languages": languages,
	}, nil
}
