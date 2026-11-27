package auth

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/the-agency-labs/axiom/server/internal/db"
)

// Cookie and header names (same as Django's, so the SPA is unchanged).
const (
	SessionCookie = "sessionid"
	CSRFCookie    = "csrftoken"
	CSRFHeader    = "X-CSRFToken"
)

const csrfMaxAge = 365 * 24 * time.Hour

// ErrInvalidCredentials is returned by Login for an unknown user, a wrong
// password or an inactive account (indistinguishable on purpose).
var ErrInvalidCredentials = errors.New("invalid username or password")

// Service holds auth state. It is safe for concurrent use.
type Service struct {
	q           *db.Queries
	sessionLife time.Duration
	now         func() time.Time
}

// NewService creates a Service. sessionDays <= 0 defaults to 14.
func NewService(q *db.Queries, sessionDays int) *Service {
	if sessionDays <= 0 {
		sessionDays = 14
	}
	return &Service{q: q, sessionLife: time.Duration(sessionDays) * 24 * time.Hour, now: func() time.Time { return time.Now().UTC() }}
}

// Principal is the authenticated caller: a user, via a session (Token == nil)
// or an API token.
type Principal struct {
	User  db.User
	Token *db.ApiToken
}

// CanWrite reports whether the caller may modify data. Sessions can; tokens
// need the write scope.
func (p *Principal) CanWrite() bool { return p.Token == nil || p.Token.Scope == ScopeWrite }

type ctxKey struct{}

// FromContext returns the caller, or nil when unauthenticated.
func FromContext(ctx context.Context) *Principal {
	p, _ := ctx.Value(ctxKey{}).(*Principal)
	return p
}

// User returns the authenticated user, or nil.
func User(ctx context.Context) *db.User {
	if p := FromContext(ctx); p != nil {
		return &p.User
	}
	return nil
}

func randomToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

var (
	dummyOnce sync.Once
	dummyHash string
)

// Login verifies the credentials and starts a session. It returns the user and
// the raw cookie value. A hash made with older parameters is replaced with a current one.
func (s *Service) Login(ctx context.Context, username, password, userAgent string) (db.User, string, error) {
	user, err := s.q.GetUserByUsername(ctx, username)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			// Spend the same time as a real check so usernames can't be probed.
			dummyOnce.Do(func() { dummyHash, _ = HashPassword("dummy") })
			VerifyPassword(password, dummyHash)
			return db.User{}, "", ErrInvalidCredentials
		}
		return db.User{}, "", err
	}
	ok, rehash := VerifyPassword(password, user.PasswordHash)
	if !ok || user.IsActive == 0 {
		return db.User{}, "", ErrInvalidCredentials
	}
	if rehash {
		if h, err := HashPassword(password); err == nil {
			if err := s.q.SetPasswordHash(ctx, db.SetPasswordHashParams{PasswordHash: h, ID: user.ID}); err == nil {
				user.PasswordHash = h
			}
		}
	}
	now := s.now()
	_ = s.q.TouchLogin(ctx, db.TouchLoginParams{LastLoginAt: sql.NullString{String: now.Format(time.RFC3339), Valid: true}, ID: user.ID})
	raw, err := randomToken()
	if err != nil {
		return db.User{}, "", err
	}
	if len(userAgent) > 255 {
		userAgent = userAgent[:255]
	}
	err = s.q.CreateSession(ctx, db.CreateSessionParams{
		IDHash: sha256Hex(raw), UserID: user.ID, CreatedAt: now.Format(time.RFC3339),
		ExpiresAt: now.Add(s.sessionLife).Format(time.RFC3339), UserAgent: userAgent,
	})
	if err != nil {
		return db.User{}, "", err
	}
	return user, raw, nil
}

// Logout deletes the session behind the request's cookie.
func (s *Service) Logout(ctx context.Context, r *http.Request) {
	if c, err := r.Cookie(SessionCookie); err == nil && c.Value != "" {
		_ = s.q.DeleteSession(ctx, sha256Hex(c.Value))
	}
}

// CleanupSessions removes expired sessions and returns how many.
func (s *Service) CleanupSessions(ctx context.Context) (int64, error) {
	return s.q.DeleteExpiredSessions(ctx, s.now().Format(time.RFC3339))
}

// authenticateSession resolves the session cookie. When less than half the
// lifetime remains the session is extended (sliding expiry) and renewed is true.
func (s *Service) authenticateSession(ctx context.Context, raw string) (p *Principal, renewed bool) {
	if raw == "" {
		return nil, false
	}
	hash := sha256Hex(raw)
	now := s.now()
	row, err := s.q.GetSessionUser(ctx, db.GetSessionUserParams{IDHash: hash, ExpiresAt: now.Format(time.RFC3339)})
	if err != nil || row.User.IsActive == 0 {
		return nil, false
	}
	if exp, err := time.Parse(time.RFC3339, row.SessionExpiresAt); err == nil && exp.Sub(now) < s.sessionLife/2 {
		err := s.q.RenewSession(ctx, db.RenewSessionParams{ExpiresAt: now.Add(s.sessionLife).Format(time.RFC3339), IDHash: hash})
		renewed = err == nil
	}
	return &Principal{User: row.User}, renewed
}

// IsSecure reports whether the request arrived over HTTPS, directly or via a
// proxy that sets X-Forwarded-Proto.
func IsSecure(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	proto, _, _ := strings.Cut(r.Header.Get("X-Forwarded-Proto"), ",")
	return strings.EqualFold(strings.TrimSpace(proto), "https")
}

// SetSessionCookie sets the session cookie for raw, valid for the session lifetime.
func (s *Service) SetSessionCookie(w http.ResponseWriter, r *http.Request, raw string) {
	http.SetCookie(w, &http.Cookie{
		Name: SessionCookie, Value: raw, Path: "/", MaxAge: int(s.sessionLife.Seconds()),
		HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: IsSecure(r),
	})
}

// ClearSessionCookie expires the session cookie.
func (s *Service) ClearSessionCookie(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name: SessionCookie, Value: "", Path: "/", MaxAge: -1,
		HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: IsSecure(r),
	})
}

// CSRFToken returns the request's CSRF token, creating the cookie when missing
// or when rotate is true (done on login).
func (s *Service) CSRFToken(w http.ResponseWriter, r *http.Request, rotate bool) (string, error) {
	if c, err := r.Cookie(CSRFCookie); err == nil && len(c.Value) >= 32 && !rotate {
		return c.Value, nil
	}
	token, err := randomToken()
	if err != nil {
		return "", err
	}
	http.SetCookie(w, &http.Cookie{
		Name: CSRFCookie, Value: token, Path: "/", MaxAge: int(csrfMaxAge.Seconds()),
		SameSite: http.SameSiteLaxMode, Secure: IsSecure(r), // readable by the SPA, so not HttpOnly
	})
	return token, nil
}

// CheckCSRF validates the double-submit token: header must equal the cookie.
func CheckCSRF(r *http.Request) bool {
	c, err := r.Cookie(CSRFCookie)
	if err != nil || c.Value == "" {
		return false
	}
	h := r.Header.Get(CSRFHeader)
	return h != "" && subtle.ConstantTimeCompare([]byte(h), []byte(c.Value)) == 1
}

// CreateUser adds an account with an argon2id password hash.
func (s *Service) CreateUser(ctx context.Context, username, password string, admin bool) (db.User, error) {
	hash, err := HashPassword(password)
	if err != nil {
		return db.User{}, err
	}
	webhook, err := randomToken()
	if err != nil {
		return db.User{}, err
	}
	var isAdmin int64
	if admin {
		isAdmin = 1
	}
	return s.q.CreateUser(ctx, db.CreateUserParams{
		Username: username, PasswordHash: hash, IsAdmin: isAdmin,
		WebhookToken: webhook, CreatedAt: s.now().Format(time.RFC3339),
	})
}

func isUnsafe(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions, http.MethodTrace:
		return false
	}
	return true
}
