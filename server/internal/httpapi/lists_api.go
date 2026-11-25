package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/the-agency-labs/encompass/server/internal/catalog"
	"github.com/the-agency-labs/encompass/server/internal/db"
	"github.com/the-agency-labs/encompass/server/internal/providers"
)

// Permission model (src/lists/models.py, plus public lists):
//   - owner: everything, including delete, visibility and members;
//   - collaborator (list_members): view, rename/describe, add/remove items,
//     reorder, manage members (Django's edit form had the collaborators field);
//   - everyone else: 404 on a private list; on a public list read-only (403 on writes).
const (
	listPrivate = "private"
	listPublic  = "public"

	listNameMax = 255
	listDescMax = 5000
)

type listAccess struct {
	row    db.GetListRowRow
	viewer int64
}

func (a listAccess) owner() bool   { return a.row.OwnerID == a.viewer }
func (a listAccess) canEdit() bool { return a.owner() || a.row.IsMember }

// listFor loads a list the viewer may see; anything else is 404.
func (s *Server) listFor(ctx context.Context, r *http.Request) (listAccess, error) {
	id, err := libraryPathInt(r, "id")
	if err != nil {
		return listAccess{}, err
	}
	viewer := overviewUser(ctx).ID
	row, err := s.Queries.GetListRow(ctx, db.GetListRowParams{ViewerID: viewer, ID: id})
	if errors.Is(err, sql.ErrNoRows) {
		return listAccess{}, Errorf(http.StatusNotFound, "List not found")
	}
	if err != nil {
		return listAccess{}, err
	}
	a := listAccess{row: row, viewer: viewer}
	if !a.canEdit() && row.Visibility != listPublic {
		return listAccess{}, Errorf(http.StatusNotFound, "List not found")
	}
	return a, nil
}

// editableList is listFor for writes: view-only callers get 403.
func (s *Server) editableList(ctx context.Context, r *http.Request) (listAccess, error) {
	a, err := s.listFor(ctx, r)
	if err != nil {
		return a, err
	}
	if !a.canEdit() {
		return a, Errorf(http.StatusForbidden, "You do not have permission to edit this list.")
	}
	return a, nil
}

func (s *Server) ownedList(ctx context.Context, r *http.Request, what string) (listAccess, error) {
	a, err := s.editableList(ctx, r)
	if err != nil {
		return a, err
	}
	if !a.owner() {
		return a, Errorf(http.StatusForbidden, "You do not have permission to "+what+" this list.")
	}
	return a, nil
}

func listHead(a listAccess) map[string]any {
	r := a.row
	return map[string]any{
		"id": r.ID, "name": r.Name, "description": r.Description, "visibility": r.Visibility,
		"owner": map[string]any{"id": r.OwnerID, "username": r.OwnerUsername}, "created_at": r.CreatedAt,
		"is_owner": a.owner(), "can_edit": a.canEdit(), "can_delete": a.owner(),
	}
}

func (s *Server) listMembers(ctx context.Context, listID int64) ([]map[string]any, error) {
	rows, err := s.Queries.ListListMembers(ctx, listID)
	if err != nil {
		return nil, err
	}
	out := make([]map[string]any, 0, len(rows))
	for _, m := range rows {
		out = append(out, map[string]any{"id": m.ID, "username": m.Username})
	}
	return out, nil
}

// listsIndex is GET /lists: lists the user owns or collaborates on.
// Query: q (name/description), sort = last_item_added (default) | name | items_count | newest_first,
// item_id (adds has_item to each row, for "add to list" menus).
func (s *Server) listsIndex(r *http.Request) (any, error) {
	ctx := r.Context()
	u := overviewUser(ctx)
	itemID, err := libraryQueryInt(r, "item_id", 0)
	if err != nil {
		return nil, err
	}
	rows, err := s.Queries.ListListsForUser(ctx, db.ListListsForUserParams{UserID: u.ID, ItemID: int64(itemID)})
	if err != nil {
		return nil, err
	}
	if q := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q"))); q != "" {
		kept := rows[:0]
		for _, l := range rows {
			if strings.Contains(strings.ToLower(l.Name), q) || strings.Contains(strings.ToLower(l.Description), q) {
				kept = append(kept, l)
			}
		}
		rows = kept
	}
	switch r.URL.Query().Get("sort") {
	case "name":
	case "items_count":
		sort.SliceStable(rows, func(i, j int) bool { return rows[i].ItemsCount > rows[j].ItemsCount })
	case "newest_first":
		sort.SliceStable(rows, func(i, j int) bool { return rows[i].ID > rows[j].ID })
	default: // last_item_added, then name (rows are already name-ordered)
		sort.SliceStable(rows, func(i, j int) bool { return rows[i].LastAddedAt > rows[j].LastAddedAt })
	}
	out := make([]map[string]any, 0, len(rows))
	for _, l := range rows {
		a := listAccess{viewer: u.ID, row: db.GetListRowRow{
			ID: l.ID, OwnerID: l.OwnerID, Name: l.Name, Description: l.Description, Visibility: l.Visibility,
			CreatedAt: l.CreatedAt, OwnerUsername: l.OwnerUsername, IsMember: l.OwnerID != u.ID,
		}}
		m := listHead(a)
		m["items_count"], m["image"], m["has_item"] = l.ItemsCount, l.Image, l.HasItem
		m["last_item_added"] = libraryStrOrNil(l.LastAddedAt)
		out = append(out, m)
	}
	return out, nil
}

type listInput struct {
	Name        *string `json:"name"`
	Description *string `json:"description"`
	Visibility  *string `json:"visibility"`
}

func (in listInput) validate() error {
	if in.Name != nil {
		n := strings.TrimSpace(*in.Name)
		if n == "" || len([]rune(n)) > listNameMax {
			return Errorf(http.StatusUnprocessableEntity, "name must be 1-255 characters")
		}
	}
	if in.Description != nil && len([]rune(*in.Description)) > listDescMax {
		return Errorf(http.StatusUnprocessableEntity, "description is too long")
	}
	if in.Visibility != nil && *in.Visibility != listPrivate && *in.Visibility != listPublic {
		return Errorf(http.StatusUnprocessableEntity, "visibility must be private or public")
	}
	return nil
}

// listsCreate is POST /lists {"name", "description"?, "visibility"?}; returns the new list.
func (s *Server) listsCreate(r *http.Request) (any, error) {
	ctx := r.Context()
	var in listInput
	if err := DecodeJSON(r, &in); err != nil {
		return nil, err
	}
	if in.Name == nil {
		return nil, Errorf(http.StatusUnprocessableEntity, "name is required")
	}
	if err := in.validate(); err != nil {
		return nil, err
	}
	p := listParams{vis: listPrivate}
	p.name = strings.TrimSpace(*in.Name)
	if in.Description != nil {
		p.desc = *in.Description
	}
	if in.Visibility != nil {
		p.vis = *in.Visibility
	}
	id, err := s.Queries.CreateList(ctx, db.CreateListParams{
		OwnerID: overviewUser(ctx).ID, Name: p.name, Description: p.desc, Visibility: p.vis,
		CreatedAt: catalog.Stamp(s.calendarNow()),
	})
	if err != nil {
		return nil, err
	}
	r.SetPathValue("id", fmt.Sprint(id))
	return s.listsDetail(r)
}

type listParams struct{ name, desc, vis string }

// listsPatch is PATCH /lists/{id}: name/description by owner or collaborator, visibility by the owner.
func (s *Server) listsPatch(r *http.Request) (any, error) {
	ctx := r.Context()
	a, err := s.editableList(ctx, r)
	if err != nil {
		return nil, err
	}
	var in listInput
	if err := DecodeJSON(r, &in); err != nil {
		return nil, err
	}
	if err := in.validate(); err != nil {
		return nil, err
	}
	if in.Visibility != nil && !a.owner() {
		return nil, Errorf(http.StatusForbidden, "Only the owner can change the visibility.")
	}
	p := listParams{name: a.row.Name, desc: a.row.Description, vis: a.row.Visibility}
	if in.Name != nil {
		p.name = strings.TrimSpace(*in.Name)
	}
	if in.Description != nil {
		p.desc = *in.Description
	}
	if in.Visibility != nil {
		p.vis = *in.Visibility
	}
	if err := s.Queries.UpdateList(ctx, db.UpdateListParams{Name: p.name, Description: p.desc, Visibility: p.vis, ID: a.row.ID}); err != nil {
		return nil, err
	}
	return s.listsDetail(r)
}

// listsDelete is DELETE /lists/{id} (owner only).
func (s *Server) listsDelete(r *http.Request) (any, error) {
	a, err := s.ownedList(r.Context(), r, "delete")
	if err != nil {
		return nil, err
	}
	if err := s.Queries.DeleteList(r.Context(), a.row.ID); err != nil {
		return nil, err
	}
	return nil, nil
}

func listItemJSON(row db.ListListItemsRow) map[string]any {
	var entry any
	if row.EntryID.Valid {
		entry = map[string]any{
			"id": row.EntryID.Int64, "status": row.EntryStatus.String,
			"score": libraryNullFloat(row.EntryScore), "progress": row.EntryProgress.Int64,
		}
	}
	return map[string]any{
		"item": libraryItemJSON(row.Item, nil), "added_at": row.AddedAt, "position": row.Position, "entry": entry,
	}
}

// listsDetail is GET /lists/{id}. Query: sort = date_added (default, newest first) | title |
// media_type | position (manual order), type, status (viewer's entry status, "All" = any),
// q (title), page, page_size (default 100, max 500).
func (s *Server) listsDetail(r *http.Request) (any, error) {
	ctx := r.Context()
	a, err := s.listFor(ctx, r)
	if err != nil {
		return nil, err
	}
	q := r.URL.Query()
	page, err := libraryQueryInt(r, "page", 1)
	if err != nil {
		return nil, err
	}
	size, err := libraryQueryInt(r, "page_size", 100)
	if err != nil {
		return nil, err
	}
	page, size = max(page, 1), overviewClamp(size, 1, 500)
	status := q.Get("status")
	if status != "" && !settingsStatuses[status] {
		return nil, Errorf(http.StatusUnprocessableEntity, "unknown status")
	}
	rows, err := s.Queries.ListListItems(ctx, db.ListListItemsParams{ViewerID: a.viewer, ListID: a.row.ID})
	if err != nil {
		return nil, err
	}
	typeSet := map[string]bool{}
	for _, row := range rows {
		typeSet[row.Item.Kind] = true
	}
	types := make([]string, 0, len(typeSet))
	for k := range typeSet {
		types = append(types, k)
	}
	sort.Strings(types)

	title := strings.ToLower(strings.TrimSpace(q.Get("q")))
	kept := rows[:0]
	for _, row := range rows {
		switch {
		case q.Get("type") != "" && q.Get("type") != "all" && row.Item.Kind != q.Get("type"):
		case title != "" && !strings.Contains(strings.ToLower(row.Item.Title), title):
		case status != "" && status != "All" && row.EntryStatus.String != status:
		default:
			kept = append(kept, row)
		}
	}
	rows = kept
	switch q.Get("sort") {
	case "title":
		sort.SliceStable(rows, func(i, j int) bool {
			return strings.ToLower(rows[i].Item.Title) < strings.ToLower(rows[j].Item.Title)
		})
	case "media_type":
		sort.SliceStable(rows, func(i, j int) bool { return rows[i].Item.Kind < rows[j].Item.Kind })
	case "position":
		sort.SliceStable(rows, func(i, j int) bool { return rows[i].Position < rows[j].Position })
	}
	total := len(rows)
	lo := min((page-1)*size, total)
	hi := min(lo+size, total)
	items := make([]map[string]any, 0, hi-lo)
	for _, row := range rows[lo:hi] {
		items = append(items, listItemJSON(row))
	}
	members, err := s.listMembers(ctx, a.row.ID)
	if err != nil {
		return nil, err
	}
	out := listHead(a)
	out["members"], out["items"], out["items_count"] = members, items, total
	out["page"], out["page_size"], out["media_types"] = page, size, types
	return out, nil
}

// flexString accepts a JSON string or number (media ids are strings in the API
// but clients send numbers for TMDB).
type flexString string

func (f *flexString) UnmarshalJSON(b []byte) error {
	var n json.Number
	if err := json.Unmarshal(b, &n); err == nil {
		*f = flexString(n)
		return nil
	}
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	*f = flexString(s)
	return nil
}

type listItemInput struct {
	ItemID    int64      `json:"item_id"`
	MediaType string     `json:"media_type"`
	Source    string     `json:"source"`
	MediaID   flexString `json:"media_id"`
}

// resolveItem finds the item named by item_id, or by (media_type, source,
// media_id) creating it from the provider when it is not known yet.
func (s *Server) resolveItem(ctx context.Context, in listItemInput, create bool) (db.Item, error) {
	if in.ItemID != 0 {
		it, err := s.Queries.GetItem(ctx, in.ItemID)
		if errors.Is(err, sql.ErrNoRows) {
			return it, Errorf(http.StatusNotFound, "Item not found")
		}
		return it, err
	}
	if in.MediaType == "" || in.Source == "" || in.MediaID == "" {
		return db.Item{}, Errorf(http.StatusUnprocessableEntity, "item_id or media_type, source and media_id are required")
	}
	if !libraryValidKind(in.MediaType) {
		return db.Item{}, Errorf(http.StatusUnprocessableEntity, "media_type must be one of the library media types")
	}
	if !libraryKnownSource(in.Source) {
		return db.Item{}, Errorf(http.StatusUnprocessableEntity, "unknown source")
	}
	if !create {
		it, err := s.Queries.GetItemByKey(ctx, db.GetItemByKeyParams{Source: in.Source, ExternalID: string(in.MediaID), Kind: in.MediaType})
		if errors.Is(err, sql.ErrNoRows) {
			return it, Errorf(http.StatusNotFound, "Item not found")
		}
		return it, err
	}
	it, err := s.Catalog.EnsureItem(ctx, in.MediaType, in.Source, string(in.MediaID))
	var perr *providers.Error
	if errors.As(err, &perr) && perr.Status == http.StatusNotFound {
		return it, Errorf(http.StatusNotFound, "Item not found at "+in.Source)
	}
	return it, libraryError(err, in.Source)
}

// listsAddItem is POST /lists/{id}/items {"item_id"} or {"media_type","source","media_id"}.
// Adding an item already in the list is a no-op. Returns the list item.
func (s *Server) listsAddItem(r *http.Request) (any, error) {
	ctx := r.Context()
	a, err := s.editableList(ctx, r)
	if err != nil {
		return nil, err
	}
	var in listItemInput
	if err := DecodeJSON(r, &in); err != nil {
		return nil, err
	}
	it, err := s.resolveItem(ctx, in, true)
	if err != nil {
		return nil, err
	}
	if _, err := s.Queries.AddListItem(ctx, db.AddListItemParams{
		ListID: a.row.ID, ItemID: it.ID, AddedAt: catalog.Stamp(s.calendarNow().Truncate(time.Second)), ListID_2: a.row.ID,
	}); err != nil {
		return nil, err
	}
	rows, err := s.Queries.ListListItems(ctx, db.ListListItemsParams{ViewerID: a.viewer, ListID: a.row.ID})
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		if row.Item.ID == it.ID {
			return listItemJSON(row), nil
		}
	}
	return nil, errors.New("lists: added item missing")
}

// listsRemoveItem is DELETE /lists/{id}/items {"item_id"} or .../items/{item_id}.
func (s *Server) listsRemoveItem(r *http.Request) (any, error) {
	ctx := r.Context()
	a, err := s.editableList(ctx, r)
	if err != nil {
		return nil, err
	}
	var in listItemInput
	if v := r.PathValue("item_id"); v != "" {
		if in.ItemID, err = libraryPathInt(r, "item_id"); err != nil {
			return nil, err
		}
	} else if err := DecodeJSON(r, &in); err != nil {
		return nil, err
	}
	it, err := s.resolveItem(ctx, in, false)
	if err != nil {
		return nil, err
	}
	n, err := s.Queries.RemoveListItem(ctx, db.RemoveListItemParams{ListID: a.row.ID, ItemID: it.ID})
	if err != nil {
		return nil, err
	}
	if n == 0 {
		return nil, Errorf(http.StatusNotFound, "Item is not in this list")
	}
	return nil, nil
}

// listsReorder is PUT /lists/{id}/order {"item_ids": [...]}: sets the manual
// order (sort=position). Items not named keep their relative order after the named ones.
func (s *Server) listsReorder(r *http.Request) (any, error) {
	ctx := r.Context()
	a, err := s.editableList(ctx, r)
	if err != nil {
		return nil, err
	}
	var in struct {
		ItemIDs []int64 `json:"item_ids"`
	}
	if err := DecodeJSON(r, &in); err != nil {
		return nil, err
	}
	current, err := s.Queries.ListListItemIDs(ctx, a.row.ID)
	if err != nil {
		return nil, err
	}
	in.ItemIDs = dedupe(in.ItemIDs)
	have := map[int64]bool{}
	for _, id := range current {
		have[id] = true
	}
	named := map[int64]bool{}
	for _, id := range in.ItemIDs {
		if !have[id] {
			return nil, Errorf(http.StatusUnprocessableEntity, fmt.Sprintf("item %d is not in this list", id))
		}
		named[id] = true
	}
	order := in.ItemIDs
	for _, id := range current {
		if !named[id] {
			order = append(order, id)
		}
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	q := s.Queries.WithTx(tx)
	for i, id := range order {
		if err := q.SetListItemPosition(ctx, db.SetListItemPositionParams{Position: int64(i + 1), ListID: a.row.ID, ItemID: id}); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	r.URL.RawQuery = "sort=position"
	return s.listsDetail(r)
}

func dedupe(ids []int64) []int64 {
	seen := map[int64]bool{}
	out := make([]int64, 0, len(ids))
	for _, id := range ids {
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out
}

type listMemberInput struct {
	Username string `json:"username"`
}

func (s *Server) memberTarget(ctx context.Context, r *http.Request) (db.User, error) {
	var in listMemberInput
	if err := DecodeJSON(r, &in); err != nil {
		return db.User{}, err
	}
	u, err := s.Queries.GetUserByUsername(ctx, strings.TrimSpace(in.Username))
	if errors.Is(err, sql.ErrNoRows) || (err == nil && u.IsActive == 0) {
		return db.User{}, Errorf(http.StatusNotFound, "User not found")
	}
	return u, err
}

// listsAddMember is POST /lists/{id}/members {"username"} (owner or collaborator).
func (s *Server) listsAddMember(r *http.Request) (any, error) {
	ctx := r.Context()
	a, err := s.editableList(ctx, r)
	if err != nil {
		return nil, err
	}
	u, err := s.memberTarget(ctx, r)
	if err != nil {
		return nil, err
	}
	if u.ID == a.row.OwnerID {
		return nil, Errorf(http.StatusUnprocessableEntity, "The owner is already part of the list")
	}
	if err := s.Queries.AddListMember(ctx, db.AddListMemberParams{ListID: a.row.ID, UserID: u.ID}); err != nil {
		return nil, err
	}
	return s.listMembers(ctx, a.row.ID)
}

// listsRemoveMember is DELETE /lists/{id}/members {"username"}: the owner or a
// collaborator removes a collaborator; a collaborator can always leave.
func (s *Server) listsRemoveMember(r *http.Request) (any, error) {
	ctx := r.Context()
	a, err := s.editableList(ctx, r)
	if err != nil {
		return nil, err
	}
	u, err := s.memberTarget(ctx, r)
	if err != nil {
		return nil, err
	}
	n, err := s.Queries.RemoveListMember(ctx, db.RemoveListMemberParams{ListID: a.row.ID, UserID: u.ID})
	if err != nil {
		return nil, err
	}
	if n == 0 {
		return nil, Errorf(http.StatusNotFound, "User is not a collaborator")
	}
	if u.ID == a.viewer {
		return []map[string]any{}, nil // left the list; it may no longer be visible
	}
	return s.listMembers(ctx, a.row.ID)
}
