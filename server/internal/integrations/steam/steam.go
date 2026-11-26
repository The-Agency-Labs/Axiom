// Package steam imports a user's owned Steam games as game entries with their
// playtime as progress. It ports src/integrations/imports/steam.py.
//
// Games are matched to IGDB through the Steam app id (IGDB external games).
// Playtime is in minutes and is never lowered: the playtime tracked here can
// include time played outside Steam. A game with no playtime is Planning, one
// played in the last two weeks In progress, one played but not recently
// Paused (also for a planned game that already exists).
package steam

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/the-agency-labs/axiom/server/internal/catalog"
	"github.com/the-agency-labs/axiom/server/internal/db"
	"github.com/the-agency-labs/axiom/server/internal/integrations/sourcestatus"
	"github.com/the-agency-labs/axiom/server/internal/jobs"
	"github.com/the-agency-labs/axiom/server/internal/library"
	"github.com/the-agency-labs/axiom/server/internal/providers"
	"github.com/the-agency-labs/axiom/server/internal/providers/igdb"
)

const (
	// Source is the source string of the importer's changes.
	Source = "steam"
	// SourceName is the importer's name in the Sources status.
	SourceName = "steam"
	// JobKind is the one-off job that syncs a user's Steam library.
	JobKind = "steam.sync"
	// DefaultBaseURL is the Steam Web API.
	DefaultBaseURL = "https://api.steampowered.com"

	label = "Steam"
	note  = "Imported from Steam"
)

// IGDB is the slice of the IGDB client the importer uses (*igdb.Client).
type IGDB interface {
	ExternalGame(ctx context.Context, externalID string, source int) (int, error)
}

// Importer imports Steam libraries.
type Importer struct {
	Q       *db.Queries
	Library *library.Service
	Catalog *catalog.Catalog
	HTTP    *providers.Client
	IGDB    IGDB
	// Key is STEAM_API_KEY.
	Key     string
	BaseURL string
	Now     func() time.Time
}

// New builds an importer. base performs the Steam calls; key is STEAM_API_KEY.
func New(cat *catalog.Catalog, lib *library.Service, q *db.Queries, base *providers.Client, key string) *Importer {
	im := &Importer{Q: q, Library: lib, Catalog: cat, HTTP: base, Key: key, BaseURL: DefaultBaseURL}
	if cat != nil && cat.Reg != nil {
		if g, ok := cat.Reg.P.IGDB.(IGDB); ok {
			im.IGDB = g
		}
	}
	return im
}

func (im *Importer) now() time.Time {
	if im.Now != nil {
		return im.Now().UTC()
	}
	return time.Now().UTC()
}

// Errors the importer reports to the user.
var (
	ErrNoKey     = errors.New("Steam API key not configured in environment variables")
	ErrNoSteamID = errors.New("no Steam ID saved")
	ErrVanity    = errors.New("Steam profile name could not be resolved")
)

// Result is what a run did.
type Result struct {
	Counts   map[string]int `json:"counts"`
	Warnings []string       `json:"warnings"`
}

type game struct {
	AppID           int64  `json:"appid"`
	Name            string `json:"name"`
	PlaytimeForever int    `json:"playtime_forever"` // minutes
	Playtime2Weeks  int    `json:"playtime_2weeks"`  // minutes
}

var steamID64 = regexp.MustCompile(`^\d{17}$`)

// gameStatus is the status Steam playtime implies.
func gameStatus(forever, twoWeeks int) string {
	switch {
	case forever == 0:
		return library.Planning
	case twoWeeks > 0:
		return library.InProgress
	}
	return library.Paused
}

// apiError turns a Steam HTTP failure into the messages users get.
func apiError(err error) error {
	var pe *providers.Error
	if errors.As(err, &pe) {
		switch pe.Status {
		case http.StatusTooManyRequests:
			return errors.New("Steam API rate limit exceeded. Please try again later")
		case http.StatusForbidden:
			return errors.New("Steam profile is private or invalid")
		case http.StatusBadRequest:
			return errors.New("Bad request to Steam API. Please check the Steam ID")
		case http.StatusUnauthorized:
			return errors.New("Invalid Steam API key")
		case 0:
			return fmt.Errorf("Steam API could not be reached: %w", pe.Err)
		}
		return fmt.Errorf("Steam API error: %d", pe.Status)
	}
	return err
}

func (im *Importer) get(ctx context.Context, path string, params url.Values, dst any) error {
	params.Set("key", im.Key)
	params.Set("format", "json")
	err := im.HTTP.JSON(ctx, providers.Request{Provider: label, URL: strings.TrimRight(im.BaseURL, "/") + path + "?" + params.Encode()}, dst)
	if err != nil {
		return apiError(err)
	}
	return nil
}

// Resolve returns the SteamID64 of a SteamID64 or vanity name.
func (im *Importer) Resolve(ctx context.Context, idOrName string) (string, error) {
	idOrName = strings.TrimSpace(idOrName)
	if steamID64.MatchString(idOrName) {
		return idOrName, nil
	}
	if idOrName == "" {
		return "", ErrNoSteamID
	}
	// Accept profile URLs too.
	if u, err := url.Parse(idOrName); err == nil && u.Host != "" {
		parts := strings.Split(strings.Trim(u.Path, "/"), "/")
		if len(parts) >= 2 && (parts[0] == "id" || parts[0] == "profiles") {
			if parts[0] == "profiles" && steamID64.MatchString(parts[1]) {
				return parts[1], nil
			}
			idOrName = parts[1]
		}
	}
	var resp struct {
		Response struct {
			Success int    `json:"success"`
			SteamID string `json:"steamid"`
		} `json:"response"`
	}
	if err := im.get(ctx, "/ISteamUser/ResolveVanityURL/v0001/", url.Values{"vanityurl": {idOrName}}, &resp); err != nil {
		return "", err
	}
	if resp.Response.Success != 1 || resp.Response.SteamID == "" {
		return "", fmt.Errorf("%w: %q", ErrVanity, idOrName)
	}
	return resp.Response.SteamID, nil
}

func (im *Importer) ownedGames(ctx context.Context, id string) ([]game, error) {
	var resp struct {
		Response *struct {
			Games *[]game `json:"games"`
		} `json:"response"`
	}
	err := im.get(ctx, "/IPlayerService/GetOwnedGames/v0001/", url.Values{
		"steamid": {id}, "include_appinfo": {"1"}, "include_played_free_games": {"1"},
	}, &resp)
	if err != nil {
		return nil, err
	}
	if resp.Response == nil {
		return nil, errors.New("Invalid response from Steam API")
	}
	if resp.Response.Games == nil {
		return nil, nil // private profile or no games
	}
	return *resp.Response.Games, nil
}

// Import imports the owned games of a SteamID64 for the user.
func (im *Importer) Import(ctx context.Context, userID int64, steamID string) (*Result, error) {
	if im.Key == "" {
		return nil, ErrNoKey
	}
	games, err := im.ownedGames(ctx, steamID)
	if err != nil {
		return nil, err
	}
	res := &Result{Counts: map[string]int{}, Warnings: []string{}}
	if len(games) == 0 {
		res.Warnings = append(res.Warnings, "No games found: the Steam profile's game details may be private")
		return res, nil
	}
	for _, g := range games {
		if ctx.Err() != nil {
			return res, ctx.Err()
		}
		name := g.Name
		if name == "" {
			name = fmt.Sprintf("Unknown Game %d", g.AppID)
		}
		if err := im.importGame(ctx, userID, g, name, res); err != nil {
			if ctx.Err() != nil {
				return res, ctx.Err()
			}
			slog.WarnContext(ctx, "steam: game failed", "name", name, "appid", g.AppID, "err", err)
			res.Warnings = append(res.Warnings, fmt.Sprintf("Failed to process Steam game %s (%d): %v", name, g.AppID, err))
		}
	}
	return res, nil
}

func (im *Importer) importGame(ctx context.Context, userID int64, g game, name string, res *Result) error {
	if im.IGDB == nil {
		return errors.New("IGDB is not configured")
	}
	igdbID, err := im.IGDB.ExternalGame(ctx, strconv.FormatInt(g.AppID, 10), igdb.SourceSteam)
	if err != nil {
		return err
	}
	if igdbID == 0 {
		return im.unmatched(res, name, g.AppID)
	}
	item, err := im.Catalog.EnsureItem(ctx, catalog.KindGame, catalog.SourceIGDB, strconv.Itoa(igdbID))
	if err != nil {
		if providers.NotFound(err) {
			return im.unmatched(res, name, g.AppID)
		}
		return err
	}
	status := gameStatus(g.PlaytimeForever, g.Playtime2Weeks)
	cur, curErr := im.Library.CurrentEntry(ctx, userID, item.ID)
	exists := curErr == nil
	if curErr != nil && !errors.Is(curErr, library.ErrNoEntry) {
		return curErr
	}
	v := library.SyncValues{Status: status, Progress: g.PlaytimeForever}
	if !exists {
		v.Notes = note
	}
	changed, err := im.Library.SyncEntry(ctx, userID, item.ID, v, Source, im.now())
	if err != nil {
		return err
	}
	// Planned games that were played but not recently are paused, like in a
	// fresh import: Paused isn't a status an import moves forward to.
	if exists && cur.Status == library.Planning && status == library.Paused {
		if err := im.Library.SetStatus(ctx, userID, item.ID, library.Paused, library.SourceManual, im.now()); err != nil {
			return err
		}
		changed = true
	}
	switch {
	case !exists:
		res.Counts["games"]++
	case changed:
		res.Counts["updated"]++
	}
	return nil
}

// unmatched skips a game IGDB doesn't know (tools, test builds, delisted
// oddities): owner rule, it is dropped quietly, not reported as a problem.
func (im *Importer) unmatched(_ *Result, name string, appID int64) error {
	slog.Debug("steam: no IGDB match, skipped", "name", name, "appid", appID)
	return nil
}

// IDKey is the kv key of a user's saved Steam id (an id or a vanity name
// until the first run resolves it).
func IDKey(userID int64) string { return fmt.Sprintf("steam:%d:id", userID) }

// SaveID stores the Steam id (or vanity name) a user wants synced.
func (im *Importer) SaveID(ctx context.Context, userID int64, idOrName string) error {
	idOrName = strings.TrimSpace(idOrName)
	if idOrName == "" {
		return ErrNoSteamID
	}
	return im.Q.SetKV(ctx, db.SetKVParams{Key: IDKey(userID), Value: idOrName, UpdatedAt: im.now().Format(time.RFC3339)})
}

// SavedID returns the saved Steam id or name of a user.
func (im *Importer) SavedID(ctx context.Context, userID int64) (string, bool) {
	v, err := im.Q.GetKV(ctx, IDKey(userID))
	if err != nil || strings.TrimSpace(v) == "" {
		return "", false
	}
	return v, true
}

// Sync imports the saved Steam library of a user: it resolves a vanity name
// (and saves the resolved id), imports, and records the Sources status.
func (im *Importer) Sync(ctx context.Context, userID int64) (*Result, error) {
	saved, ok := im.SavedID(ctx, userID)
	if !ok {
		return nil, ErrNoSteamID
	}
	var res *Result
	err := func() error {
		if im.Key == "" {
			return ErrNoKey
		}
		id, err := im.Resolve(ctx, saved)
		if err != nil {
			return err
		}
		if id != saved {
			if err := im.SaveID(ctx, userID, id); err != nil {
				return err
			}
		}
		res, err = im.Import(ctx, userID, id)
		return err
	}()
	at := im.now()
	var st sourcestatus.Status
	if err != nil {
		st = sourcestatus.New(SourceName, at, nil, nil, err.Error(), false)
	} else {
		st = sourcestatus.New(SourceName, at, res.Counts, res.Warnings, "", len(res.Warnings) == 0)
		if len(res.Warnings) > 0 {
			st.Message = fmt.Sprintf("%d games had problems", len(res.Warnings))
		}
	}
	wctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if werr := sourcestatus.Write(wctx, im.Q, userID, st); werr != nil {
		slog.ErrorContext(ctx, "steam: write source status", "err", werr)
	}
	return res, err
}

var savedKey = regexp.MustCompile(`^steam:(\d+):id$`)

// UsersWithSavedID lists the users that saved a Steam id.
func (im *Importer) UsersWithSavedID(ctx context.Context) ([]int64, error) {
	rows, err := im.Q.ListKVByPrefix(ctx, "steam:")
	if err != nil {
		return nil, err
	}
	var ids []int64
	for _, r := range rows {
		if m := savedKey.FindStringSubmatch(r.Key); m != nil && strings.TrimSpace(r.Value) != "" {
			if id, err := strconv.ParseInt(m[1], 10, 64); err == nil {
				ids = append(ids, id)
			}
		}
	}
	return ids, nil
}

// RegisterJobs schedules steam.sync daily for every user that saved a Steam
// id (when the API key is set), and handles one-off "steam.sync" jobs.
func (im *Importer) RegisterJobs(m *jobs.Manager) {
	m.Daily("steam.sync", 4, 30, func(ctx context.Context) error {
		if im.Key == "" {
			return nil
		}
		users, err := im.UsersWithSavedID(ctx)
		if err != nil {
			return err
		}
		var errs []error
		for _, id := range users {
			if _, err := im.Sync(ctx, id); err != nil {
				errs = append(errs, fmt.Errorf("user %d: %w", id, err))
			}
		}
		return errors.Join(errs...)
	})
	m.Handle(JobKind, func(ctx context.Context, job jobs.Job) (string, error) {
		res, err := im.Sync(ctx, job.UserID)
		if errors.Is(err, ErrNoSteamID) || errors.Is(err, ErrNoKey) || errors.Is(err, ErrVanity) {
			return "", nil // retrying can't help; the status says why
		}
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("%v", res.Counts), nil
	})
}
