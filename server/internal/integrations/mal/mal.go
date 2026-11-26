// Package mal imports a public MyAnimeList list (anime and manga) into a
// user's library. It ports src/integrations/imports/mal.py (from the Django
// app's history): the list is read from the MyAnimeList API with MAL_API and
// handed to the AniList importer, which places anime in their TVDB series (or
// keeps them as count-based MAL anime) and files manga as count-based entries.
//
// Like every importer here it is additive: it adds entries and moves statuses
// forward, and never removes or lowers anything.
package mal

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/the-agency-labs/axiom/server/internal/db"
	"github.com/the-agency-labs/axiom/server/internal/integrations/anilist"
	"github.com/the-agency-labs/axiom/server/internal/integrations/sourcestatus"
	"github.com/the-agency-labs/axiom/server/internal/jobs"
	"github.com/the-agency-labs/axiom/server/internal/providers"
)

const (
	// Source is the source string of the importer's changes.
	Source = "mal"
	// SourceName is the importer's name in the Integrations status.
	SourceName = "mal"
	// JobKind is the one-off job the API enqueues ({"username"}).
	JobKind = "mal.import"
	// DefaultBaseURL is the MyAnimeList API.
	DefaultBaseURL = "https://api.myanimelist.net/v2/users"

	label    = "MyAnimeList"
	pageSize = 1000
	maxPages = 50 // a hard stop against a paging loop
)

// ErrNoKey means MAL_API is not set.
var ErrNoKey = errors.New("MyAnimeList API key not configured in environment variables")

// ErrNoUser means the MyAnimeList username is missing or unknown.
var ErrNoUser = errors.New("MyAnimeList user not found")

var usernameRe = regexp.MustCompile(`^[A-Za-z0-9_-]{2,16}$`)

// Importer imports MyAnimeList lists.
type Importer struct {
	Q       *db.Queries
	Entries *anilist.Importer
	HTTP    *providers.Client
	Key     string
	BaseURL string
	Now     func() time.Time
}

// New builds an importer. key is MAL_API.
func New(entries *anilist.Importer, q *db.Queries, base *providers.Client, key string) *Importer {
	return &Importer{Q: q, Entries: entries, HTTP: base, Key: key, BaseURL: DefaultBaseURL}
}

func (im *Importer) now() time.Time {
	if im.Now != nil {
		return im.Now().UTC()
	}
	return time.Now().UTC()
}

type listStatus struct {
	Status       string  `json:"status"`
	Score        float64 `json:"score"`
	Watched      int     `json:"num_episodes_watched"`
	ChaptersRead int     `json:"num_chapters_read"`
	Rewatching   bool    `json:"is_rewatching"`
	Rereading    bool    `json:"is_rereading"`
	Comments     string  `json:"comments"`
	StartDate    string  `json:"start_date"`
	FinishDate   string  `json:"finish_date"`
	UpdatedAt    string  `json:"updated_at"`
}

type node struct {
	ID          int64  `json:"id"`
	Title       string `json:"title"`
	MainPicture struct {
		Large string `json:"large"`
	} `json:"main_picture"`
	MediaType   string `json:"media_type"`
	NumEpisodes int    `json:"num_episodes"`
	NumChapters int    `json:"num_chapters"`
	StartDate   string `json:"start_date"`
}

type listItem struct {
	Node       node       `json:"node"`
	ListStatus listStatus `json:"list_status"`
}

type page struct {
	Data   []listItem `json:"data"`
	Paging struct {
		Next string `json:"next"`
	} `json:"paging"`
}

// Preflight rejects a missing key or an implausible username before any work.
func (im *Importer) Preflight(username string) error {
	if im.Key == "" {
		return ErrNoKey
	}
	if !usernameRe.MatchString(strings.TrimSpace(username)) {
		return fmt.Errorf("%w: %q", ErrNoUser, username)
	}
	return nil
}

func (im *Importer) fetch(ctx context.Context, username, kind string) ([]listItem, error) {
	fields := "num_episodes,num_chapters,media_type,start_date,list_status{comments,num_times_rewatched,num_times_reread}"
	next := strings.TrimRight(im.BaseURL, "/") + "/" + url.PathEscape(username) + "/" + kind + "list?" + url.Values{
		"fields": {fields}, "nsfw": {"true"}, "limit": {fmt.Sprint(pageSize)},
	}.Encode()
	var out []listItem
	for i := 0; next != "" && i < maxPages; i++ {
		var p page
		err := im.HTTP.JSON(ctx, providers.Request{
			Provider: label, URL: next, Header: http.Header{"X-Mal-Client-Id": {im.Key}},
		}, &p)
		if err != nil {
			var pe *providers.Error
			if errors.As(err, &pe) && pe.Status == http.StatusNotFound {
				return nil, fmt.Errorf("%w: %q", ErrNoUser, username)
			}
			if errors.As(err, &pe) && (pe.Status == http.StatusForbidden || pe.Status == http.StatusUnauthorized) {
				return nil, errors.New("MyAnimeList refused the request: the list is private or the API key is invalid")
			}
			return nil, err
		}
		out = append(out, p.Data...)
		next = p.Paging.Next
	}
	return out, nil
}

// anilistStatus maps a MyAnimeList status to the one the shared importer reads.
func anilistStatus(s string, repeating bool) string {
	if repeating {
		return "REPEATING"
	}
	switch s {
	case "completed":
		return "COMPLETED"
	case "watching", "reading":
		return "CURRENT"
	case "on_hold":
		return "PAUSED"
	case "dropped":
		return "DROPPED"
	}
	return "PLANNING" // plan_to_watch, plan_to_read
}

// parseDate reads MAL's YYYY, YYYY-MM or YYYY-MM-DD (a missing part is 1).
func parseDate(s string) anilist.FuzzyDate {
	var d anilist.FuzzyDate
	switch len(s) {
	case 4:
		fmt.Sscanf(s, "%d", &d.Year)
	case 7:
		fmt.Sscanf(s, "%d-%d", &d.Year, &d.Month)
	case 10:
		fmt.Sscanf(s, "%d-%d-%d", &d.Year, &d.Month, &d.Day)
	}
	return d
}

func convert(it listItem, anime bool) anilist.Entry {
	ls := it.ListStatus
	id := it.Node.ID
	e := anilist.Entry{
		Status: anilistStatus(ls.Status, ls.Rewatching || ls.Rereading),
		Score:  ls.Score, // MyAnimeList scores unrated media as 0
		Notes:  ls.Comments, StartedAt: parseDate(ls.StartDate), CompletedAt: parseDate(ls.FinishDate),
	}
	if anime {
		e.Progress = ls.Watched
	} else {
		e.Progress = ls.ChaptersRead
	}
	if t, err := time.Parse(time.RFC3339, ls.UpdatedAt); err == nil {
		e.UpdatedAt = t.Unix()
	}
	e.Media.IDMal = &id // no AniList id: the entry is looked up and kept by its MAL id
	e.Media.Title.UserPreferred = it.Node.Title
	e.Media.CoverImage.Large = it.Node.MainPicture.Large
	e.Media.StartDate = parseDate(it.Node.StartDate)
	if anime {
		if it.Node.MediaType == "movie" {
			e.Media.Format = "MOVIE"
		}
		if it.Node.NumEpisodes > 0 {
			n := it.Node.NumEpisodes
			e.Media.Episodes = &n
		}
	} else if it.Node.NumChapters > 0 {
		n := it.Node.NumChapters
		e.Media.Chapters = &n
	}
	return e
}

// Run imports the whole list of a MyAnimeList user. With dryRun it writes
// nothing and the result lists what would change.
func (im *Importer) Run(ctx context.Context, userID int64, username string, dryRun bool) (*anilist.Result, error) {
	username = strings.TrimSpace(username)
	if err := im.Preflight(username); err != nil {
		return nil, err
	}
	animeItems, err := im.fetch(ctx, username, "anime")
	if err != nil {
		return nil, err
	}
	mangaItems, err := im.fetch(ctx, username, "manga")
	if err != nil {
		return nil, err
	}
	anime := make([]anilist.Entry, 0, len(animeItems))
	for _, it := range animeItems {
		anime = append(anime, convert(it, true))
	}
	manga := make([]anilist.Entry, 0, len(mangaItems))
	for _, it := range mangaItems {
		manga = append(manga, convert(it, false))
	}
	return im.Entries.RunEntries(ctx, userID, anime, manga, anilist.Options{DryRun: dryRun, Source: Source})
}

// Import runs the import the way the job does: it records the Integrations status.
func (im *Importer) Import(ctx context.Context, userID int64, username string) (*anilist.Result, error) {
	res, err := im.Run(ctx, userID, username, false)
	at := im.now()
	var st sourcestatus.Status
	if err != nil {
		st = sourcestatus.New(SourceName, at, nil, nil, err.Error(), false)
	} else {
		st = sourcestatus.New(SourceName, at, res.Counts, res.Warnings, "", len(res.Warnings) == 0)
		if len(res.Warnings) > 0 {
			st.Message = fmt.Sprintf("%d entries had problems", len(res.Warnings))
		}
	}
	wctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if werr := sourcestatus.Write(wctx, im.Q, userID, st); werr != nil {
		slog.ErrorContext(ctx, "mal: write source status", "err", werr)
	}
	return res, err
}

// RegisterJobs handles the one-off "mal.import" jobs the API enqueues.
func (im *Importer) RegisterJobs(m *jobs.Manager) {
	m.Handle(JobKind, func(ctx context.Context, job jobs.Job) (string, error) {
		var p struct {
			Username string `json:"username"`
		}
		if err := json.Unmarshal(job.Payload, &p); err != nil || p.Username == "" {
			return "", errors.New("mal: job has no username")
		}
		res, err := im.Import(ctx, job.UserID, p.Username)
		if errors.Is(err, ErrNoKey) || errors.Is(err, ErrNoUser) {
			return "", nil // retrying can't help; the status says why
		}
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("%v", res.Counts), nil
	})
}
