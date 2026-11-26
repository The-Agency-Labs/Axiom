package httpapi

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"errors"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/the-agency-labs/encompass/server/internal/auth"
	"github.com/the-agency-labs/encompass/server/internal/catalog"
	"github.com/the-agency-labs/encompass/server/internal/db"
	"github.com/the-agency-labs/encompass/server/internal/library"
)

const (
	settingsMinPassword   = 8
	settingsDefaultSort   = "year"      // grouped by the year finished (games: year added)
	settingsDefaultStatus = "Completed" // games have no status: "All"
)

var (
	settingsSorts    = map[string]bool{"year": true, "activity": true, "score": true, "title": true, "progress": true, "start_date": true, "end_date": true}
	settingsStatuses = map[string]bool{"All": true, library.Planning: true, library.InProgress: true, library.Paused: true, library.Dropped: true, library.Completed: true}
	settingsLang     = regexp.MustCompile(`^[A-Za-z]{2,3}(-[A-Za-z0-9]{2,8})*$`)
)

type settingsMedia struct {
	Kind    string `json:"kind"`
	Enabled bool   `json:"enabled"`
	Sort    string `json:"sort"`
	Status  string `json:"status"`
}

// settingsRoutes registers the settings, password, webhook token and user
// management endpoints. All are browser-session only (no API tokens).
func (s *Server) settingsRoutes(mux *http.ServeMux) {
	session := func(h func(r *http.Request) (any, error)) http.Handler {
		return auth.RequireSession(JSON(func(_ http.ResponseWriter, r *http.Request) (any, error) { return h(r) }))
	}
	admin := func(h func(r *http.Request) (any, error)) http.Handler {
		return session(func(r *http.Request) (any, error) {
			if overviewUser(r.Context()).IsAdmin == 0 {
				return nil, Errorf(http.StatusForbidden, "Admin access required")
			}
			return h(r)
		})
	}
	mux.Handle("GET /api/v1/settings", session(s.settingsGet))
	mux.Handle("PATCH /api/v1/settings", session(s.settingsPatch))
	mux.Handle("POST /api/v1/settings/password", session(s.settingsPassword))
	mux.Handle("POST /api/v1/settings/webhook-token", session(s.settingsWebhookToken))
	mux.Handle("GET /api/v1/users", admin(s.settingsListUsers))
	mux.Handle("POST /api/v1/users", admin(s.settingsCreateUser))
	mux.Handle("PATCH /api/v1/users/{id}", admin(s.settingsPatchUser))
}

// settingsMediaTypes returns every kind with the user's choices (defaults for missing rows).
func (s *Server) settingsMediaTypes(ctx context.Context, userID int64) ([]settingsMedia, error) {
	rows, err := s.Queries.ListUserMediaSettings(ctx, userID)
	if err != nil {
		return nil, err
	}
	have := map[string]db.UserMediaSetting{}
	for _, r := range rows {
		have[r.Kind] = r
	}
	out := make([]settingsMedia, 0, len(catalog.Kinds))
	for _, k := range catalog.Kinds {
		m := settingsMedia{Kind: k, Enabled: true, Sort: settingsDefaultSort, Status: settingsDefaultStatus}
		if k == catalog.KindGame || k == catalog.KindBoardgame {
			m.Status = "All" // no status: games and board games are owned and played, not finished
		}
		if r, ok := have[k]; ok {
			m = settingsMedia{Kind: k, Enabled: r.Enabled != 0, Sort: r.Sort, Status: r.Status}
		}
		out = append(out, m)
	}
	return out, nil
}

func settingsPaths(u db.User) map[string]any {
	return map[string]any{
		"webhook_url_path":  "/webhook/jellyfin/" + u.WebhookToken,
		"calendar_url_path": "/calendar/" + u.CalendarToken.String + ".ics",
	}
}

func (s *Server) settingsView(ctx context.Context, u db.User) (map[string]any, error) {
	media, err := s.settingsMediaTypes(ctx, u.ID)
	if err != nil {
		return nil, err
	}
	out := settingsPaths(u)
	out["username"], out["is_admin"] = u.Username, u.IsAdmin != 0
	out["timezone"], out["language"] = u.Timezone, u.Language
	out["media_types"], out["jellyfin_users"], out["watched_percent"] = media, u.JellyfinUsers, u.WatchedPercent
	prof := s.loadProfile(ctx, u.ID)
	out["display_name"], out["avatar"] = prof.DisplayName, prof.Avatar
	return out, nil
}

func (s *Server) settingsGet(r *http.Request) (any, error) {
	return s.settingsView(r.Context(), overviewUser(r.Context()))
}

func (s *Server) settingsPatch(r *http.Request) (any, error) {
	ctx := r.Context()
	u := overviewUser(ctx)
	var in struct {
		Timezone       *string `json:"timezone"`
		Language       *string `json:"language"`
		JellyfinUsers  *string `json:"jellyfin_users"`
		WatchedPercent *int    `json:"watched_percent"`
		DisplayName    *string `json:"display_name"`
		Avatar         *string `json:"avatar"`
		MediaTypes     []struct {
			Kind    string  `json:"kind"`
			Enabled *bool   `json:"enabled"`
			Sort    *string `json:"sort"`
			Status  *string `json:"status"`
		} `json:"media_types"`
	}
	if err := DecodeJSON(r, &in); err != nil {
		return nil, err
	}
	// Validate everything before writing anything.
	if in.Timezone != nil {
		if _, err := time.LoadLocation(*in.Timezone); err != nil || *in.Timezone == "" || *in.Timezone == "Local" {
			return nil, Errorf(http.StatusUnprocessableEntity, "Unknown time zone")
		}
		u.Timezone = *in.Timezone
	}
	if in.Language != nil {
		if !settingsLang.MatchString(*in.Language) {
			return nil, Errorf(http.StatusUnprocessableEntity, "Invalid language code")
		}
		u.Language = *in.Language
	}
	if in.JellyfinUsers != nil {
		parts := []string{}
		for _, p := range strings.Split(*in.JellyfinUsers, ",") {
			if p = strings.TrimSpace(p); p != "" {
				parts = append(parts, p)
			}
		}
		u.JellyfinUsers = strings.Join(parts, ", ")
		if len(u.JellyfinUsers) > 500 {
			return nil, Errorf(http.StatusUnprocessableEntity, "jellyfin_users is too long")
		}
	}
	if in.WatchedPercent != nil {
		u.WatchedPercent = int64(overviewClamp(*in.WatchedPercent, 50, 100))
	}
	prof := s.loadProfile(ctx, u.ID)
	if in.DisplayName != nil {
		name, err := validateName(*in.DisplayName)
		if err != nil {
			return nil, err
		}
		prof.DisplayName = name
	}
	if in.Avatar != nil {
		if err := validateAvatar(*in.Avatar); err != nil {
			return nil, err
		}
		prof.Avatar = *in.Avatar
	}
	current, err := s.settingsMediaTypes(ctx, u.ID)
	if err != nil {
		return nil, err
	}
	byKind := map[string]*settingsMedia{}
	for i := range current {
		byKind[current[i].Kind] = &current[i]
	}
	changed := map[string]bool{}
	for _, m := range in.MediaTypes {
		cur, ok := byKind[m.Kind]
		if !ok {
			return nil, Errorf(http.StatusUnprocessableEntity, "Unknown media type: "+m.Kind)
		}
		if m.Enabled != nil {
			cur.Enabled = *m.Enabled
		}
		if m.Sort != nil {
			if !settingsSorts[*m.Sort] {
				return nil, Errorf(http.StatusUnprocessableEntity, "Invalid sort: "+*m.Sort)
			}
			cur.Sort = *m.Sort
		}
		if m.Status != nil {
			if !settingsStatuses[*m.Status] {
				return nil, Errorf(http.StatusUnprocessableEntity, "Invalid status: "+*m.Status)
			}
			cur.Status = *m.Status
		}
		changed[m.Kind] = true
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback() //nolint:errcheck
	q := s.Queries.WithTx(tx)
	if err := q.UpdateUserSettings(ctx, db.UpdateUserSettingsParams{
		Timezone: u.Timezone, Language: u.Language, JellyfinUsers: u.JellyfinUsers, WatchedPercent: u.WatchedPercent, ID: u.ID,
	}); err != nil {
		return nil, err
	}
	if in.DisplayName != nil || in.Avatar != nil {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO user_profiles (user_id, display_name, avatar) VALUES (?, ?, ?)
			ON CONFLICT(user_id) DO UPDATE SET display_name = excluded.display_name, avatar = excluded.avatar`,
			u.ID, prof.DisplayName, prof.Avatar); err != nil {
			return nil, err
		}
	}
	for _, m := range current {
		if !changed[m.Kind] {
			continue
		}
		var enabled int64
		if m.Enabled {
			enabled = 1
		}
		if err := q.UpsertUserMediaSetting(ctx, db.UpsertUserMediaSettingParams{UserID: u.ID, Kind: m.Kind, Enabled: enabled, Sort: m.Sort, Status: m.Status}); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	fresh, err := s.Queries.GetUser(ctx, u.ID)
	if err != nil {
		return nil, err
	}
	return s.settingsView(ctx, fresh)
}

func (s *Server) settingsPassword(r *http.Request) (any, error) {
	u := overviewUser(r.Context())
	var in struct {
		Current string `json:"current"`
		New     string `json:"new"`
	}
	if err := DecodeJSON(r, &in); err != nil {
		return nil, err
	}
	if len(in.New) < settingsMinPassword {
		return nil, Errorf(http.StatusUnprocessableEntity, "New password must be at least "+strconv.Itoa(settingsMinPassword)+" characters")
	}
	if ok, _ := auth.VerifyPassword(in.Current, u.PasswordHash); !ok {
		return nil, Errorf(http.StatusBadRequest, "Current password is incorrect")
	}
	hash, err := auth.HashPassword(in.New)
	if err != nil {
		return nil, err
	}
	if err := s.Queries.SetPasswordHash(r.Context(), db.SetPasswordHashParams{PasswordHash: hash, ID: u.ID}); err != nil {
		return nil, err
	}
	return map[string]bool{"ok": true}, nil
}

func (s *Server) settingsWebhookToken(r *http.Request) (any, error) {
	u := overviewUser(r.Context())
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return nil, err
	}
	u.WebhookToken = base64.RawURLEncoding.EncodeToString(b)
	if err := s.Queries.SetWebhookToken(r.Context(), db.SetWebhookTokenParams{WebhookToken: u.WebhookToken, ID: u.ID}); err != nil {
		return nil, err
	}
	return settingsPaths(u), nil
}

func settingsUserJSON(u db.User) map[string]any {
	var last any
	if u.LastLoginAt.Valid {
		last = u.LastLoginAt.String
	}
	return map[string]any{
		"id": u.ID, "username": u.Username, "is_admin": u.IsAdmin != 0, "is_active": u.IsActive != 0,
		"created_at": u.CreatedAt, "last_login_at": last,
	}
}

func (s *Server) settingsListUsers(r *http.Request) (any, error) {
	users, err := s.Queries.ListUsers(r.Context())
	if err != nil {
		return nil, err
	}
	out := make([]map[string]any, len(users))
	for i, u := range users {
		out[i] = settingsUserJSON(u)
	}
	return out, nil
}

func (s *Server) settingsCreateUser(r *http.Request) (any, error) {
	var in struct {
		Username string `json:"username"`
		Password string `json:"password"`
		IsAdmin  bool   `json:"is_admin"`
	}
	if err := DecodeJSON(r, &in); err != nil {
		return nil, err
	}
	in.Username = strings.TrimSpace(in.Username)
	if in.Username == "" || len(in.Username) > 150 {
		return nil, Errorf(http.StatusUnprocessableEntity, "Username must be 1-150 characters")
	}
	if len(in.Password) < settingsMinPassword {
		return nil, Errorf(http.StatusUnprocessableEntity, "Password must be at least "+strconv.Itoa(settingsMinPassword)+" characters")
	}
	if _, err := s.Queries.GetUserByUsername(r.Context(), in.Username); err == nil {
		return nil, Errorf(http.StatusConflict, "Username already taken")
	} else if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	u, err := s.Auth.CreateUser(r.Context(), in.Username, in.Password, in.IsAdmin)
	if err != nil {
		return nil, err
	}
	return settingsUserJSON(u), nil
}

func (s *Server) settingsPatchUser(r *http.Request) (any, error) {
	ctx := r.Context()
	me := overviewUser(ctx)
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		return nil, Errorf(http.StatusUnprocessableEntity, "user id must be an integer")
	}
	var in struct {
		IsActive *bool `json:"is_active"`
		IsAdmin  *bool `json:"is_admin"`
	}
	if err := DecodeJSON(r, &in); err != nil {
		return nil, err
	}
	target, err := s.Queries.GetUser(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, Errorf(http.StatusNotFound, "Not Found")
	}
	if err != nil {
		return nil, err
	}
	if id == me.ID && ((in.IsActive != nil && !*in.IsActive) || (in.IsAdmin != nil && !*in.IsAdmin)) {
		return nil, Errorf(http.StatusBadRequest, "You can't deactivate or demote yourself")
	}
	if in.IsActive != nil {
		target.IsActive = settingsBool(*in.IsActive)
	}
	if in.IsAdmin != nil {
		target.IsAdmin = settingsBool(*in.IsAdmin)
	}
	if err := s.Queries.SetUserFlags(ctx, db.SetUserFlagsParams{IsActive: target.IsActive, IsAdmin: target.IsAdmin, ID: id}); err != nil {
		return nil, err
	}
	if target.IsActive == 0 {
		if err := s.Queries.DeleteUserSessions(ctx, id); err != nil {
			return nil, err
		}
	}
	return settingsUserJSON(target), nil
}

func settingsBool(b bool) int64 {
	if b {
		return 1
	}
	return 0
}
