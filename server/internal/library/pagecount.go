package library

import (
	"context"
	"database/sql"
	"math"
	"strconv"

	"github.com/the-agency-labs/axiom/server/internal/catalog"
	"github.com/the-agency-labs/axiom/server/internal/db"
)

// SetPageCount makes pages the length of a book (the count the reader's device
// uses) and re-expresses every entry's progress, and its progress history, in
// those pages, so the fraction read stays the same. Nothing is logged: the
// reader did nothing. Other kinds, and an unchanged count, are left alone.
func (s *Service) SetPageCount(ctx context.Context, itemID, pages int64) error {
	if pages <= 0 {
		return nil
	}
	tx, err := s.Conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	q := s.Q.WithTx(tx)
	item, err := q.GetItem(ctx, itemID)
	if err != nil {
		return err
	}
	old := item.Total.Int64
	if item.Kind != catalog.KindBook || old == pages {
		return nil
	}
	entries, err := q.ListItemEntries(ctx, itemID)
	if err != nil {
		return err
	}
	for _, e := range entries {
		progress := rescalePage(e.Progress, old, pages)
		if e.Status == Completed && e.Progress > 0 {
			progress = pages
		}
		if progress != e.Progress {
			if err := q.UpdateEntry(ctx, db.UpdateEntryParams{
				Status: e.Status, Score: e.Score, Progress: progress, ProgressedAt: e.ProgressedAt,
				StartedAt: e.StartedAt, FinishedAt: e.FinishedAt, Notes: e.Notes, UpdatedAt: e.UpdatedAt, ID: e.ID,
			}); err != nil {
				return err
			}
		}
		if old <= 0 {
			continue // the history was never measured against a length
		}
		events, err := q.ListEntryEvents(ctx, e.ID)
		if err != nil {
			return err
		}
		convert := func(v sql.NullString) sql.NullString {
			if n, err := strconv.ParseInt(v.String, 10, 64); v.Valid && err == nil {
				v.String = strconv.FormatInt(rescalePage(n, old, pages), 10)
			}
			return v
		}
		for _, ev := range events {
			if ev.Field != "progress" {
				continue
			}
			if err := q.UpdateEntryEventValues(ctx, db.UpdateEntryEventValuesParams{Old: convert(ev.Old), New: convert(ev.New), ID: ev.ID}); err != nil {
				return err
			}
		}
	}
	if err := q.UpdateItemTotal(ctx, db.UpdateItemTotalParams{Total: sql.NullInt64{Int64: pages, Valid: true}, ID: itemID}); err != nil {
		return err
	}
	return tx.Commit()
}

// rescalePage moves a page of a book of old pages into one of pages pages. The
// start stays the start, the end the end, and a page in between stays inside.
// With no old length the page is only kept within the new one.
func rescalePage(page, old, pages int64) int64 {
	switch {
	case page <= 0:
		return 0
	case old <= 0:
		return min(page, pages)
	case page >= old:
		return pages
	}
	n := int64(math.RoundToEven(float64(page) * float64(pages) / float64(old)))
	return max(1, min(n, pages-1))
}
