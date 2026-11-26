package httpapi

import (
	"context"
	"encoding/base64"
	"net/http"
	"regexp"
	"strings"
	"unicode/utf8"
)

const (
	profileNameMax   = 60
	profileAvatarMax = 256 << 10 // the browser sends ~256px JPEGs, well under this
)

var profileAvatarRe = regexp.MustCompile(`^data:image/(?:jpeg|png|webp);base64,([A-Za-z0-9+/]+=*)$`)

// profile is a user's optional display name and picture (user_profiles).
type profile struct {
	DisplayName string
	Avatar      string
}

func (s *Server) loadProfile(ctx context.Context, userID int64) profile {
	var p profile
	// No row means no profile yet; a failed read just shows the username.
	_ = s.DB.QueryRowContext(ctx, `SELECT display_name, avatar FROM user_profiles WHERE user_id = ?`, userID).
		Scan(&p.DisplayName, &p.Avatar)
	return p
}

// validateName trims and checks a display name; empty clears it.
func validateName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if utf8.RuneCountInString(name) > profileNameMax {
		return "", Errorf(http.StatusUnprocessableEntity, "Name is too long")
	}
	return name, nil
}

// validateAvatar accepts "" (remove) or a small base64 image data URL.
func validateAvatar(v string) error {
	if v == "" {
		return nil
	}
	if len(v) > profileAvatarMax {
		return Errorf(http.StatusUnprocessableEntity, "Picture is too large")
	}
	m := profileAvatarRe.FindStringSubmatch(v)
	if m == nil {
		return Errorf(http.StatusUnprocessableEntity, "Picture must be a JPEG, PNG or WebP image")
	}
	if _, err := base64.StdEncoding.DecodeString(m[1]); err != nil {
		return Errorf(http.StatusUnprocessableEntity, "Picture is not valid")
	}
	return nil
}
