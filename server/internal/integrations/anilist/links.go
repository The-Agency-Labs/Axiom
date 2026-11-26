package anilist

import (
	"context"
	"errors"
	"log/slog"
	"strconv"

	"github.com/the-agency-labs/axiom/server/internal/catalog"
	"github.com/the-agency-labs/axiom/server/internal/library"
)

// linkManga records the tracked manga on the user's AniList list, which
// Paperback writes to, as tracked there.
func (im *Importer) linkManga(ctx context.Context, userID int64, manga []entry) error {
	var ids []int64
	for _, e := range manga {
		keys := [][2]string{{catalog.SourceAniList, strconv.FormatInt(e.Media.ID, 10)}}
		if e.Media.IDMal != nil && *e.Media.IDMal != 0 {
			keys = append([][2]string{{catalog.SourceMAL, strconv.FormatInt(*e.Media.IDMal, 10)}}, keys...)
		}
		for _, k := range keys {
			item, err := im.Catalog.FindByExternalID(ctx, k[0], k[1], catalog.KindManga)
			if errors.Is(err, catalog.ErrNotFound) {
				continue
			}
			if err != nil {
				return err
			}
			ids = append(ids, item.ID)
			break
		}
	}
	return im.Library.ReplaceLinks(ctx, userID, library.LinkPaperback, ids)
}

// RefreshLinks re-reads what Paperback tracks for every mapped user, without
// importing anything.
func (im *Importer) RefreshLinks(ctx context.Context) {
	for _, p := range im.Users {
		u, err := im.Q.GetUserByUsername(ctx, p.Local)
		if err == nil {
			var manga []entry
			if _, manga, err = im.fetchLists(ctx, p.External); err == nil {
				err = im.linkManga(ctx, u.ID, manga)
			}
		}
		if err != nil {
			slog.WarnContext(ctx, "anilist: links", "user", p.Local, "err", err)
		}
	}
}
