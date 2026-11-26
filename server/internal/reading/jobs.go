package reading

import (
	"context"
	"log/slog"
	"time"

	"github.com/the-agency-labs/axiom/server/internal/jobs"
)

// AutoLinkEvery is how often documents that have never had a link attempt
// (imported ones, or uploads made while the providers were unreachable) are
// tried again. It runs soon after start the first time.
const AutoLinkEvery = 6 * time.Hour

// RegisterJobs schedules the background auto-link of unlinked documents.
func (s *Service) RegisterJobs(m *jobs.Manager) {
	m.Every("reading-auto-link", AutoLinkEvery, s.AutoLinkAllUsers)
}

// AutoLinkAllUsers runs AutoLinkPending for every active user.
func (s *Service) AutoLinkAllUsers(ctx context.Context) error {
	users, err := s.Q.ListUsers(ctx)
	if err != nil {
		return err
	}
	for _, u := range users {
		if u.IsActive == 0 {
			continue
		}
		linked, tried, err := s.AutoLinkPending(ctx, u.ID)
		if err != nil {
			return err
		}
		if tried > 0 {
			slog.InfoContext(ctx, "reading: background auto-link", "user", u.ID, "tried", tried, "linked", linked)
		}
	}
	return nil
}
