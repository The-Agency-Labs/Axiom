package httpapi

import (
	"net/http"
	"strings"

	"github.com/the-agency-labs/axiom/server/internal/auth"
)

// overrideHeader says the user was asked and wants the hand-made change.
const overrideHeader = "X-Axiom-Override"

// guardLinked refuses a hand-made progress or completion change to a title an
// app tracks (that app is the source of truth), unless the request carries the
// override header. The app asks the user, then repeats the request with it.
func (s *Server) guardLinked(r *http.Request, itemID int64) error {
	if r.Header.Get(overrideHeader) != "" {
		return nil
	}
	links, err := s.Library.Links(r.Context(), auth.User(r.Context()).ID)
	if err != nil {
		return err
	}
	if names := links[itemID]; len(names) > 0 {
		return &Error{Status: http.StatusConflict, Code: "linked", Sources: names,
			Detail: strings.Join(names, " and ") + " tracks this title"}
	}
	return nil
}
