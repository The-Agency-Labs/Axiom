package jellyfin

import (
	"context"
	"errors"
	"log/slog"

	"github.com/the-agency-labs/axiom/server/internal/catalog"
	"github.com/the-agency-labs/axiom/server/internal/library"
)

// linkLibrary records every tracked show and film in the users' Jellyfin
// libraries as tracked by Jellyfin, replacing what was recorded before. On any
// error the previous record stays.
func (s *Service) linkLibrary(ctx context.Context, userID int64, client *Client, jfUsers []string) error {
	var ids []int64
	for _, jf := range jfUsers {
		jfID, err := client.UserID(ctx, jf)
		if err != nil {
			return err
		}
		items, err := client.Items(ctx, jfID, "Series,Movie", nil)
		if err != nil {
			return err
		}
		for _, it := range items {
			kinds, sources := []string{catalog.KindTV, catalog.KindAnime}, []string{"tvdb", "tmdb", "imdb"}
			if it.Type == "Movie" {
				kinds, sources = []string{catalog.KindMovie, catalog.KindAnimeMovie}, []string{"tmdb", "imdb", "tvdb"}
			}
			for _, source := range sources {
				id := it.ProviderIDs[map[string]string{"tvdb": "Tvdb", "tmdb": "Tmdb", "imdb": "Imdb"}[source]]
				if id == "" {
					continue
				}
				found, err := s.Catalog.FindByExternalID(ctx, source, id, kinds...)
				if errors.Is(err, catalog.ErrNotFound) {
					continue
				}
				if err != nil {
					return err
				}
				ids = append(ids, found.ID)
				break
			}
		}
	}
	return s.Library.ReplaceLinks(ctx, userID, library.LinkJellyfin, ids)
}

// RefreshLinks re-reads what Jellyfin tracks for every mapped user.
func (s *Service) RefreshLinks(ctx context.Context) {
	if !s.Configured() {
		return
	}
	users, err := s.mapped(ctx)
	if err != nil {
		slog.WarnContext(ctx, "jellyfin: links", "err", err)
		return
	}
	client, err := NewClient(s.Config.JellyfinURL, s.Config.JellyfinKey, s.HTTP)
	if err != nil {
		slog.WarnContext(ctx, "jellyfin: links", "err", err)
		return
	}
	for userID, names := range users {
		if err := s.linkLibrary(ctx, userID, client, names); err != nil {
			slog.WarnContext(ctx, "jellyfin: links", "user", userID, "err", err)
		}
	}
}

// link marks a title Jellyfin just reported; a failure only delays it to the
// next library sync.
func (s *Service) link(ctx context.Context, userID, itemID int64) {
	if err := s.Library.AddLink(ctx, userID, itemID, library.LinkJellyfin); err != nil {
		slog.WarnContext(ctx, "jellyfin: link", "item", itemID, "err", err)
	}
}
