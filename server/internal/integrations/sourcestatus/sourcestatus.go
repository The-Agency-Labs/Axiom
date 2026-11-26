// Package sourcestatus stores the outcome of the last run of each importer
// (kv "source:<user_id>:<name>") for the Sources page. Importers write it
// after every run; GET /api/v1/sources reads it.
package sourcestatus

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/the-agency-labs/axiom/server/internal/db"
)

// MaxFailures caps the failure strings kept per run.
const MaxFailures = 50

// Status is the JSON stored for a source.
type Status struct {
	Name     string         `json:"name"`
	LastRun  string         `json:"last_run"` // RFC3339
	OK       bool           `json:"ok"`
	Counts   map[string]int `json:"counts"`
	Failures []string       `json:"failures"`
	Message  string         `json:"message"`
}

// Key is the kv key of a user's source status.
func Key(userID int64, name string) string { return fmt.Sprintf("source:%d:%s", userID, name) }

// New builds a Status for a run that ended at `at`.
func New(name string, at time.Time, counts map[string]int, failures []string, message string, ok bool) Status {
	if counts == nil {
		counts = map[string]int{}
	}
	if len(failures) > MaxFailures {
		failures = failures[:MaxFailures]
	}
	if failures == nil {
		failures = []string{}
	}
	return Status{Name: name, LastRun: at.UTC().Format(time.RFC3339), OK: ok, Counts: counts, Failures: failures, Message: message}
}

// Write stores the status.
func Write(ctx context.Context, q *db.Queries, userID int64, s Status) error {
	b, err := json.Marshal(s)
	if err != nil {
		return err
	}
	return q.SetKV(ctx, db.SetKVParams{Key: Key(userID, s.Name), Value: string(b), UpdatedAt: s.LastRun})
}

// Read returns the stored status, or nil when the source never ran.
func Read(ctx context.Context, q *db.Queries, userID int64, name string) (json.RawMessage, error) {
	v, err := q.GetKV(ctx, Key(userID, name))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !json.Valid([]byte(v)) {
		return nil, nil
	}
	return json.RawMessage(v), nil
}

// Pair is one "external user:axiom user" mapping (ANILIST_USERS, JELLYFIN_USERS).
type Pair struct{ External, Local string }

// ParsePairs reads "external:local" entries; an entry without a colon maps to
// the same name on both sides.
func ParsePairs(entries []string) []Pair {
	var out []Pair
	for _, e := range entries {
		ext, local, found := strings.Cut(e, ":")
		ext, local = strings.TrimSpace(ext), strings.TrimSpace(local)
		if !found {
			local = ext
		}
		if ext != "" && local != "" {
			out = append(out, Pair{External: ext, Local: local})
		}
	}
	return out
}

// HasLocal reports whether an Axiom user appears in the pairs.
func HasLocal(pairs []Pair, username string) bool {
	for _, p := range pairs {
		if strings.EqualFold(p.Local, username) {
			return true
		}
	}
	return false
}
