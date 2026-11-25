package auth

import (
	"context"
	"crypto/md5" //nolint:gosec // KOSync protocol sends md5(key)
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	"github.com/the-agency-labs/encompass/server/internal/db"
)

// TokenPrefix is the public prefix of every API key.
const TokenPrefix = "enc_"

// Token scopes.
const (
	ScopeRead  = "read"
	ScopeWrite = "write"
)

// touchInterval limits last_used_at writes to one per minute per token.
const touchInterval = time.Minute

// ErrNotFound is returned when a record doesn't exist or isn't the user's.
var ErrNotFound = errors.New("not found")

func sha256Hex(v string) string {
	sum := sha256.Sum256([]byte(v))
	return hex.EncodeToString(sum[:])
}

func md5Hex(v string) string {
	sum := md5.Sum([]byte(v)) //nolint:gosec
	return hex.EncodeToString(sum[:])
}

// TokenView is the JSON shape of an API token (never includes the key).
type TokenView struct {
	ID         int64   `json:"id"`
	Name       string  `json:"name"`
	Scope      string  `json:"scope"`
	Prefix     string  `json:"prefix"`
	CreatedAt  string  `json:"created_at"`
	LastUsedAt *string `json:"last_used_at"`
	Revoked    bool    `json:"revoked"`
}

// NewToken is TokenView plus the raw key, returned once at creation.
type NewToken struct {
	TokenView
	Key string `json:"key"`
}

func tokenView(t db.ApiToken) TokenView {
	v := TokenView{ID: t.ID, Name: t.Name, Scope: t.Scope, Prefix: t.Prefix, CreatedAt: t.CreatedAt, Revoked: t.RevokedAt.Valid}
	if t.LastUsedAt.Valid {
		v.LastUsedAt = &t.LastUsedAt.String
	}
	return v
}

// CreateToken makes a token and returns it with its raw key.
func (s *Service) CreateToken(ctx context.Context, userID int64, name, scope string) (NewToken, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return NewToken{}, err
	}
	key := TokenPrefix + base64.RawURLEncoding.EncodeToString(raw) // same shape as python's token_urlsafe(32)
	row, err := s.q.CreateAPIToken(ctx, db.CreateAPITokenParams{
		UserID: userID, Name: name, Scope: scope,
		Prefix:  key[:len(TokenPrefix)+6],
		KeyHash: sha256Hex(key), KeyMd5: md5Hex(key),
		CreatedAt: s.now().Format(time.RFC3339),
	})
	if err != nil {
		return NewToken{}, err
	}
	return NewToken{TokenView: tokenView(row), Key: key}, nil
}

// ListTokens returns the user's tokens, newest first.
func (s *Service) ListTokens(ctx context.Context, userID int64) ([]TokenView, error) {
	rows, err := s.q.ListAPITokens(ctx, userID)
	if err != nil {
		return nil, err
	}
	out := make([]TokenView, 0, len(rows))
	for _, r := range rows {
		out = append(out, tokenView(r))
	}
	return out, nil
}

// RevokeToken revokes one of the user's tokens; ErrNotFound if it isn't theirs.
func (s *Service) RevokeToken(ctx context.Context, userID, id int64) (TokenView, error) {
	row, err := s.q.RevokeAPIToken(ctx, db.RevokeAPITokenParams{
		RevokedAt: sql.NullString{String: s.now().Format(time.RFC3339), Valid: true}, ID: id, UserID: userID,
	})
	if errors.Is(err, sql.ErrNoRows) {
		return TokenView{}, ErrNotFound
	}
	if err != nil {
		return TokenView{}, err
	}
	return tokenView(row), nil
}

// authenticateToken resolves a raw bearer key; nil if invalid, revoked or the user is inactive.
func (s *Service) authenticateToken(ctx context.Context, raw string) *Principal {
	if raw == "" {
		return nil
	}
	row, err := s.q.GetAPITokenByHash(ctx, sha256Hex(raw))
	if err != nil {
		return nil
	}
	return s.tokenPrincipal(ctx, row.ApiToken, row.User)
}

// AuthenticateMD5 resolves a KOSync x-auth-user / x-auth-key pair.
func (s *Service) AuthenticateMD5(ctx context.Context, username, keyMD5 string) *Principal {
	if username == "" || keyMD5 == "" {
		return nil
	}
	row, err := s.q.GetAPITokenByMD5(ctx, db.GetAPITokenByMD5Params{Username: username, KeyMd5: strings.ToLower(keyMD5)})
	if err != nil {
		return nil
	}
	return s.tokenPrincipal(ctx, row.ApiToken, row.User)
}

func (s *Service) tokenPrincipal(ctx context.Context, t db.ApiToken, u db.User) *Principal {
	if u.IsActive == 0 {
		return nil
	}
	now := s.now()
	last, err := time.Parse(time.RFC3339, t.LastUsedAt.String)
	if !t.LastUsedAt.Valid || err != nil || now.Sub(last) > touchInterval {
		_ = s.q.TouchAPIToken(ctx, db.TouchAPITokenParams{
			LastUsedAt: sql.NullString{String: now.Format(time.RFC3339), Valid: true}, ID: t.ID,
		})
	}
	return &Principal{User: u, Token: &t}
}
