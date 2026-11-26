package reading

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"

	"github.com/the-agency-labs/axiom/server/internal/catalog"
	"github.com/the-agency-labs/axiom/server/internal/db"
	"github.com/the-agency-labs/axiom/server/internal/library"
	"github.com/the-agency-labs/axiom/server/internal/providers"
	"github.com/the-agency-labs/axiom/server/internal/providers/openlibrary"
)

// Purposes a read-through can have (Triple P).
var Purposes = []string{"educate", "entertain", "persuade"}

// MaxNoteLength caps each note field.
const MaxNoteLength = 50000

// Notes is the Triple P + Reflect state of one read-through.
type Notes struct {
	Preview     string  `json:"preview"`
	Predict     string  `json:"predict"`
	Purpose     *string `json:"purpose"`
	PurposeNote string  `json:"purpose_note"`
	Reflect     string  `json:"reflect"`
	UpdatedAt   string  `json:"updated_at"`
	// PromptPredict is true when the app should ask for a prediction and a
	// purpose: the entry is In progress and both are still empty.
	PromptPredict bool `json:"prompt_predict"`
}

// NotesUpdate is a partial update; nil fields are left alone.
type NotesUpdate struct {
	Preview     *string
	Predict     *string
	PurposeSet  bool
	Purpose     *string
	PurposeNote *string
	Reflect     *string
}

func (s *Service) bookEntry(ctx context.Context, userID, entryID int64) (library.Entry, catalog.Item, error) {
	entry, err := s.Library.GetEntry(ctx, userID, entryID)
	if errors.Is(err, library.ErrNoEntry) {
		return entry, catalog.Item{}, ErrNotFound
	}
	if err != nil {
		return entry, catalog.Item{}, err
	}
	item, err := s.Catalog.GetItem(ctx, entry.ItemID)
	if err != nil {
		return entry, item, err
	}
	if item.Kind != catalog.KindBook {
		return entry, item, ErrNotFound
	}
	return entry, item, nil
}

// SeedPreview builds the initial Preview of a book: its synopsis and, for
// books with an Open Library record, the table of contents. complete is false
// when the Open Library lookup failed (so the result may lack the contents).
func (s *Service) SeedPreview(ctx context.Context, item catalog.Item) (preview string, complete bool) {
	synopsis := item.Synopsis
	var toc []openlibrary.TOCEntry
	complete = true

	olID := ""
	if item.Source == catalog.SourceOpenLibrary {
		olID = item.ExternalID
	} else if ids, err := s.Catalog.ItemIDs(ctx, item.ID); err == nil {
		if v := ids[catalog.SourceOpenLibrary]; len(v) > 0 {
			olID = v[0]
		}
	}
	if olID != "" {
		meta, err := s.Catalog.Reg.Metadata(ctx, catalog.KindBook, catalog.SourceOpenLibrary, olID)
		if err != nil {
			if !errors.Is(err, catalog.ErrUnsupported) {
				slog.WarnContext(ctx, "reading: open library lookup for preview failed", "item", item.ID, "err", err)
				complete = false
			}
		} else {
			toc = openlibrary.TOCFromMetadata(meta)
			if synopsis == "" {
				synopsis = providers.Str(meta, "synopsis")
			}
		}
	}
	return openlibrary.PreviewText(synopsis, toc), complete
}

func promptPredict(status string, n Notes) bool {
	return status == library.InProgress && n.Predict == "" && n.Purpose == nil
}

func toNotes(row db.BookNote, status string) Notes {
	n := Notes{Preview: row.Preview, Predict: row.Predict, PurposeNote: row.PurposeNote, Reflect: row.Reflect, UpdatedAt: row.UpdatedAt}
	if row.Purpose.Valid {
		p := row.Purpose.String
		n.Purpose = &p
	}
	n.PromptPredict = promptPredict(status, n)
	return n
}

// GetNotes returns the notes of a read-through (an entry of a book). The
// row is created on first use with the Preview seeded from the book's
// metadata; if the table of contents couldn't be fetched the seeded preview
// is returned without saving, so a later read retries.
func (s *Service) GetNotes(ctx context.Context, userID, entryID int64) (Notes, error) {
	entry, item, err := s.bookEntry(ctx, userID, entryID)
	if err != nil {
		return Notes{}, err
	}
	row, err := s.Q.GetBookNotes(ctx, entryID)
	if err == nil {
		return toNotes(row, entry.Status), nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return Notes{}, err
	}
	preview, complete := s.SeedPreview(ctx, item)
	row = db.BookNote{EntryID: entryID, Preview: preview, UpdatedAt: Stamp(s.now())}
	if complete {
		if row, err = s.insertNotes(ctx, row); err != nil {
			return Notes{}, err
		}
	}
	return toNotes(row, entry.Status), nil
}

func (s *Service) insertNotes(ctx context.Context, row db.BookNote) (db.BookNote, error) {
	err := s.Q.InsertBookNotes(ctx, db.InsertBookNotesParams{
		EntryID: row.EntryID, Preview: row.Preview, Predict: row.Predict, Purpose: row.Purpose,
		PurposeNote: row.PurposeNote, Reflect: row.Reflect, UpdatedAt: row.UpdatedAt,
	})
	if err != nil {
		return row, err
	}
	return s.Q.GetBookNotes(ctx, row.EntryID) // another request may have won the insert
}

// ValidateNotes checks an update without applying it.
func ValidateNotes(u NotesUpdate) error {
	for name, v := range map[string]*string{"preview": u.Preview, "predict": u.Predict, "purpose_note": u.PurposeNote, "reflect": u.Reflect} {
		if v != nil && len([]rune(*v)) > MaxNoteLength {
			return fmt.Errorf("%w: %s is too long", ErrInvalid, name)
		}
	}
	if u.PurposeSet && u.Purpose != nil {
		ok := false
		for _, p := range Purposes {
			ok = ok || p == *u.Purpose
		}
		if !ok {
			return fmt.Errorf("%w: purpose must be educate, entertain, persuade or null", ErrInvalid)
		}
	}
	return nil
}

// UpdateNotes applies a partial update, creating the row (seeded) when needed.
func (s *Service) UpdateNotes(ctx context.Context, userID, entryID int64, u NotesUpdate) (Notes, error) {
	if err := ValidateNotes(u); err != nil {
		return Notes{}, err
	}
	entry, item, err := s.bookEntry(ctx, userID, entryID)
	if err != nil {
		return Notes{}, err
	}
	row, err := s.Q.GetBookNotes(ctx, entryID)
	if errors.Is(err, sql.ErrNoRows) {
		preview, _ := s.SeedPreview(ctx, item)
		row, err = s.insertNotes(ctx, db.BookNote{EntryID: entryID, Preview: preview, UpdatedAt: Stamp(s.now())})
	}
	if err != nil {
		return Notes{}, err
	}
	if u.Preview != nil {
		row.Preview = *u.Preview
	}
	if u.Predict != nil {
		row.Predict = *u.Predict
	}
	if u.PurposeSet {
		row.Purpose = sql.NullString{}
		if u.Purpose != nil {
			row.Purpose = sql.NullString{String: *u.Purpose, Valid: true}
		}
	}
	if u.PurposeNote != nil {
		row.PurposeNote = *u.PurposeNote
	}
	if u.Reflect != nil {
		row.Reflect = *u.Reflect
	}
	row.UpdatedAt = Stamp(s.now())
	err = s.Q.UpdateBookNotes(ctx, db.UpdateBookNotesParams{
		Preview: row.Preview, Predict: row.Predict, Purpose: row.Purpose, PurposeNote: row.PurposeNote,
		Reflect: row.Reflect, UpdatedAt: row.UpdatedAt, EntryID: entryID,
	})
	if err != nil {
		return Notes{}, err
	}
	return toNotes(row, entry.Status), nil
}
