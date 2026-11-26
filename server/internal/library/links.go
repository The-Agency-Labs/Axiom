package library

import (
	"context"

	"github.com/the-agency-labs/axiom/server/internal/db"
)

// The apps that can track a title. Kindle is read from the reading documents;
// the others are recorded by their syncs (see ReplaceLinks).
const (
	LinkJellyfin  = "jellyfin"
	LinkPaperback = "paperback" // through the AniList list Paperback writes to
	LinkSteam     = "steam"
)

var linkNames = map[string]string{LinkJellyfin: "Jellyfin", LinkPaperback: "Paperback", LinkSteam: "Steam"}

// Links lists, for each of the user's titles an app tracks, the names of
// those apps. Such an app is the source of truth for the title's progress.
func (s *Service) Links(ctx context.Context, userID int64) (map[int64][]string, error) {
	rows, err := s.Q.ListItemLinks(ctx, userID)
	if err != nil {
		return nil, err
	}
	out := map[int64][]string{}
	for _, r := range rows {
		if name := linkNames[r.Source]; name != "" {
			out[r.ItemID] = append(out[r.ItemID], name)
		}
	}
	books, err := s.Q.ListKindleItems(ctx, userID)
	if err != nil {
		return nil, err
	}
	for _, id := range books {
		out[id] = append(out[id], "Kindle")
	}
	return out, nil
}

// AddLink records that an app tracks a title.
func (s *Service) AddLink(ctx context.Context, userID, itemID int64, source string) error {
	return s.Q.AddItemLink(ctx, db.AddItemLinkParams{UserID: userID, ItemID: itemID, Source: source})
}

// ReplaceLinks makes items the whole set of titles source tracks for the user.
func (s *Service) ReplaceLinks(ctx context.Context, userID int64, source string, items []int64) error {
	tx, err := s.Conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	q := s.Q.WithTx(tx)
	if err := q.DeleteSourceLinks(ctx, db.DeleteSourceLinksParams{UserID: userID, Source: source}); err != nil {
		return err
	}
	for _, id := range items {
		if err := q.AddItemLink(ctx, db.AddItemLinkParams{UserID: userID, ItemID: id, Source: source}); err != nil {
			return err
		}
	}
	return tx.Commit()
}
