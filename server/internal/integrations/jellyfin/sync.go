package jellyfin

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/the-agency-labs/encompass/server/internal/db"
	"github.com/the-agency-labs/encompass/server/internal/jobs"
)

const (
	// JobSync is the one-off job kind that imports a user's history (job
	// user_id = the Encompass user, 0 = every mapped user). Payload: {"full": bool}.
	JobSync = "jellyfin.sync"
	// SyncEvery is how often the sync runs for every mapped user.
	SyncEvery = 6 * time.Hour

	maxFailures = 50
)

// RegisterJobs schedules the 6-hourly sync and registers the on-demand job.
func (s *Service) RegisterJobs(m *jobs.Manager) {
	m.Every("jellyfin-sync", SyncEvery, func(ctx context.Context) error {
		_, err := s.SyncAll(ctx, false)
		return err
	})
	m.Handle(JobSync, func(ctx context.Context, j jobs.Job) (string, error) {
		var p struct {
			Full bool `json:"full"`
		}
		_ = json.Unmarshal(j.Payload, &p)
		if j.UserID == 0 {
			return s.SyncAll(ctx, p.Full)
		}
		u, err := s.Q.GetUser(ctx, j.UserID)
		if err != nil {
			return "", err
		}
		return s.SyncUser(ctx, u, p.Full)
	})
}

// Configured reports whether the server URL and API key are set.
func (s *Service) Configured() bool { return s.Config.JellyfinURL != "" && s.Config.JellyfinKey != "" }

// Status is the JSON stored in kv "source:<user_id>:jellyfin".
type Status struct {
	Name     string         `json:"name"`
	LastRun  string         `json:"last_run"`
	OK       bool           `json:"ok"`
	Counts   map[string]int `json:"counts"`
	Failures []string       `json:"failures"`
	Message  string         `json:"message"`
}

// WebhookStatus is the JSON stored in kv "source:<user_id>:jellyfin_webhook".
type WebhookStatus struct {
	LastEvent string `json:"last_event"`
	LastItem  string `json:"last_item"`
	OK        bool   `json:"ok"`
}

func (s *Service) setKV(ctx context.Context, key string, v any) {
	b, err := json.Marshal(v)
	if err == nil {
		err = s.Q.SetKV(ctx, db.SetKVParams{Key: key, Value: string(b), UpdatedAt: s.Now().UTC().Format(time.RFC3339)})
	}
	if err != nil {
		slog.WarnContext(ctx, "jellyfin: kv write failed", "key", key, "err", err)
	}
}

// RecordWebhook updates the webhook status of the Sources page.
func (s *Service) RecordWebhook(ctx context.Context, userID int64, label string, ok bool) {
	s.setKV(ctx, fmt.Sprintf("source:%d:jellyfin_webhook", userID), WebhookStatus{
		LastEvent: s.Now().UTC().Format(time.RFC3339), LastItem: label, OK: ok,
	})
}

// ImportResult is what one history import did.
type ImportResult struct {
	Counts   map[string]int // processed items by Jellyfin type
	Failures []string       // per-item problems (errors and unmatched items)
	Latest   time.Time      // newest LastPlayedDate handled
}

// ImportHistory replays the user's played movies and episodes through the
// webhook processor, oldest first, with the real LastPlayedDate and Import
// mode. It is additive only. since limits it to plays after that moment.
// One bad item does not stop the run.
func (s *Service) ImportHistory(ctx context.Context, u db.User, c *Client, jellyfinUser string, since time.Time) (ImportResult, error) {
	res := ImportResult{Counts: map[string]int{"Episode": 0, "Movie": 0}}
	jfID, err := c.UserID(ctx, jellyfinUser)
	if err != nil {
		return res, err
	}
	series, err := c.Items(ctx, jfID, "Series", nil)
	if err != nil {
		return res, err
	}
	seriesTvdb := map[string]string{}
	for _, sr := range series {
		seriesTvdb[sr.ID] = sr.ProviderIDs["Tvdb"]
	}
	all, err := c.Items(ctx, jfID, "Movie,Episode", url.Values{"Filters": {"IsPlayed"}})
	if err != nil {
		return res, err
	}
	type play struct {
		item Item
		at   time.Time
	}
	var plays []play
	for _, it := range all {
		if !it.UserData.Played {
			continue
		}
		at, _ := time.Parse(time.RFC3339Nano, it.UserData.LastPlayedDate)
		if !since.IsZero() && !at.After(since) {
			continue // also skips undated plays: nothing says they are new
		}
		plays = append(plays, play{it, at.UTC()})
	}
	sort.SliceStable(plays, func(i, j int) bool {
		a, b := plays[i], plays[j]
		if !a.at.Equal(b.at) {
			return a.at.Before(b.at)
		}
		if x, y := deref(a.item.ParentIndexNumber), deref(b.item.ParentIndexNumber); x != y {
			return x < y
		}
		return deref(a.item.IndexNumber) < deref(b.item.IndexNumber)
	})
	for _, p := range plays {
		ev := Event{
			Kind: EventStop, Type: p.item.Type, Name: p.item.Name, Year: p.item.ProductionYear,
			SeriesName: p.item.SeriesName, Season: p.item.ParentIndexNumber, Episode: p.item.IndexNumber,
			Tmdb: p.item.ProviderIDs["Tmdb"], Imdb: p.item.ProviderIDs["Imdb"], Tvdb: p.item.ProviderIDs["Tvdb"],
			SeriesTvdb: seriesTvdb[p.item.SeriesID], Played: true,
		}
		r, err := s.Process(ctx, u, ev, Options{WatchedAt: p.at, Import: true})
		switch {
		case err != nil:
			slog.WarnContext(ctx, "jellyfin import failed for item", "item", ev.Label(), "err", err)
			res.Failures = append(res.Failures, fmt.Sprintf("%s: %v", ev.Label(), err))
			continue
		case r.Outcome == Unmatched:
			res.Failures = append(res.Failures, ev.Label()+": no matching title")
		}
		res.Counts[p.item.Type]++
		if p.at.After(res.Latest) {
			res.Latest = p.at
		}
	}
	return res, nil
}

// mapped lists, per Encompass user id, the Jellyfin usernames whose history
// to import: JELLYFIN_USERS ("jfuser:encompassuser", or one name when both
// match) plus the usernames a user set in Settings.
func (s *Service) mapped(ctx context.Context) (map[int64][]string, error) {
	users, err := s.Q.ListUsers(ctx)
	if err != nil {
		return nil, err
	}
	byName := map[string]db.User{}
	out := map[int64][]string{}
	add := func(id int64, name string) {
		for _, have := range out[id] {
			if strings.EqualFold(have, name) {
				return
			}
		}
		out[id] = append(out[id], name)
	}
	for _, u := range users {
		byName[strings.ToLower(u.Username)] = u
		for _, n := range strings.Split(u.JellyfinUsers, ",") {
			if n = strings.TrimSpace(n); n != "" {
				add(u.ID, n)
			}
		}
	}
	for _, entry := range s.Config.JellyfinUsers {
		jf, enc, _ := strings.Cut(strings.TrimSpace(entry), ":")
		jf, enc = strings.TrimSpace(jf), strings.TrimSpace(enc)
		if jf == "" {
			continue
		}
		if enc == "" {
			enc = jf
		}
		u, ok := byName[strings.ToLower(enc)]
		if !ok {
			slog.WarnContext(ctx, "jellyfin sync: no Encompass user", "user", enc)
			continue
		}
		add(u.ID, jf)
	}
	return out, nil
}

// SyncAll imports every mapped user's history; a no-op when Jellyfin is not
// configured.
func (s *Service) SyncAll(ctx context.Context, full bool) (string, error) {
	if !s.Configured() {
		return "Jellyfin sync not configured", nil
	}
	m, err := s.mapped(ctx)
	if err != nil {
		return "", err
	}
	ids := make([]int64, 0, len(m))
	for id := range m {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	var parts []string
	for _, id := range ids {
		u, err := s.Q.GetUser(ctx, id)
		if err != nil {
			continue
		}
		msg, _ := s.syncUser(ctx, u, m[id], full)
		parts = append(parts, u.Username+": "+msg)
	}
	if len(parts) == 0 {
		return "No JELLYFIN_USERS configured", nil
	}
	return strings.Join(parts, "; "), nil
}

// SyncUser imports one user's Jellyfin history (incremental unless full) and
// records the result for the Sources page.
func (s *Service) SyncUser(ctx context.Context, u db.User, full bool) (string, error) {
	m, err := s.mapped(ctx)
	if err != nil {
		return "", err
	}
	return s.syncUser(ctx, u, m[u.ID], full)
}

func (s *Service) markerKey(userID int64, index int, jfUser string) string {
	if index == 0 {
		return fmt.Sprintf("jellyfin:%d:last_played", userID)
	}
	return fmt.Sprintf("jellyfin:%d:%s:last_played", userID, strings.ToLower(jfUser))
}

func (s *Service) syncUser(ctx context.Context, u db.User, jfUsers []string, full bool) (string, error) {
	st := Status{Name: "jellyfin", Counts: map[string]int{"Episode": 0, "Movie": 0}, Failures: []string{}}
	finish := func(msg string, ok bool) (string, error) {
		st.LastRun = s.Now().UTC().Format(time.RFC3339)
		st.OK, st.Message = ok, msg
		total := len(st.Failures)
		if total > maxFailures {
			st.Failures = st.Failures[:maxFailures]
		}
		s.setKV(ctx, fmt.Sprintf("source:%d:jellyfin", u.ID), st)
		return msg, nil
	}
	if !s.Configured() {
		return finish("Jellyfin is not configured (JELLYFIN_URL, JELLYFIN_API_KEY)", false)
	}
	if len(jfUsers) == 0 {
		return finish("No Jellyfin user is mapped to this account", false)
	}
	client, err := NewClient(s.Config.JellyfinURL, s.Config.JellyfinKey, s.HTTP)
	if err != nil {
		return finish(err.Error(), false)
	}
	ok := true
	for i, jf := range jfUsers {
		key := s.markerKey(u.ID, i, jf)
		var since time.Time
		if !full {
			if v, err := s.Q.GetKV(ctx, key); err == nil {
				since, _ = time.Parse(time.RFC3339Nano, v)
			} else if !errors.Is(err, sql.ErrNoRows) {
				return finish(err.Error(), false)
			}
		}
		res, err := s.ImportHistory(ctx, u, client, jf, since)
		for k, n := range res.Counts {
			st.Counts[k] += n
		}
		st.Failures = append(st.Failures, res.Failures...)
		if err != nil {
			ok = false
			st.Failures = append(st.Failures, fmt.Sprintf("%s: %v", jf, err))
			continue
		}
		if len(res.Failures) > 0 {
			ok = false
		}
		if res.Latest.After(since) {
			if err := s.Q.SetKV(ctx, db.SetKVParams{Key: key, Value: res.Latest.Format(time.RFC3339Nano), UpdatedAt: s.Now().UTC().Format(time.RFC3339)}); err != nil {
				slog.WarnContext(ctx, "jellyfin: marker write failed", "key", key, "err", err)
			}
		}
	}
	msg := fmt.Sprintf("%d episodes, %d movies imported, %d problems", st.Counts["Episode"], st.Counts["Movie"], len(st.Failures))
	return finish(msg, ok)
}
