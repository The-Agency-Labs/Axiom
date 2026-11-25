package jellyfin

import (
	"fmt"
	"strconv"
	"strings"
)

// Event kinds the processor acts on.
const (
	EventPlay = "Play"
	EventStop = "Stop"
)

// Event is a normalized Jellyfin playback event: what the webhook plugin
// sends (either template) or what the history import builds from an item.
type Event struct {
	Kind       string // Play, Stop, or anything else (ignored)
	Type       string // Episode or Movie (anything else is ignored)
	Name       string
	Year       *int
	SeriesName string
	Season     *int // Jellyfin ParentIndexNumber
	Episode    *int // Jellyfin IndexNumber
	Tmdb       string
	Imdb       string
	Tvdb       string
	SeriesTmdb string // TMDB id of the series (import only)
	Played     bool
}

// Label is the human name of the item for logs and the Sources page.
func (e Event) Label() string {
	if e.Type == "Episode" {
		return fmt.Sprintf("%s S%02dE%02d", e.SeriesName, deref(e.Season), deref(e.Episode))
	}
	switch {
	case e.Name != "" && e.Year != nil && *e.Year != 0:
		return fmt.Sprintf("%s (%d)", e.Name, *e.Year)
	case e.Name != "":
		return e.Name
	}
	return "unknown item"
}

func deref(p *int) int {
	if p == nil {
		return 0
	}
	return *p
}

// notificationEvents maps the plugin's plain-template NotificationType to
// our event kinds; other notifications pass through and are ignored.
var notificationEvents = map[string]string{
	"PlaybackStart": EventPlay,
	"PlaybackStop":  EventStop,
}

// ParsePayload normalizes a webhook body. It accepts the official plugin's
// plain template (NotificationType, numbers and Played as strings) and the
// legacy one (Event, typed values). percent is the share of the runtime a
// Stop must reach to count as watched even when Jellyfin did not flag it
// played (Jellyfin only reports PlayedToCompletion past its own resume
// threshold, 90% by default).
func ParsePayload(payload map[string]any, percent int) Event {
	item := asMap(payload["Item"])
	ev := Event{
		Kind:       str(payload["Event"]),
		Type:       str(item["Type"]),
		Name:       str(item["Name"]),
		Year:       asInt(item["ProductionYear"]),
		SeriesName: str(item["SeriesName"]),
		Season:     asInt(item["ParentIndexNumber"]),
		Episode:    asInt(item["IndexNumber"]),
	}
	if ev.Kind == "" {
		n := str(payload["NotificationType"])
		if k, ok := notificationEvents[n]; ok {
			n = k
		}
		ev.Kind = n
	}
	ids := asMap(item["ProviderIds"])
	ev.Tmdb, ev.Imdb, ev.Tvdb = str(ids["Tmdb"]), str(ids["Imdb"]), str(ids["Tvdb"])
	ev.SeriesTmdb = str(asMap(asMap(payload["Series"])["ProviderIds"])["Tmdb"])
	ev.Played = asBool(asMap(item["UserData"])["Played"])
	if ev.Kind == EventStop && watchedEnough(item, percent) {
		ev.Played = true
	}
	return ev
}

func watchedEnough(item map[string]any, percent int) bool {
	pos, run := asInt(item["PlaybackPositionTicks"]), asInt(item["RunTimeTicks"])
	if pos == nil || run == nil || *pos == 0 || *run == 0 {
		return false
	}
	return int64(*pos)*100 >= int64(*run)*int64(percent)
}

func asMap(v any) map[string]any {
	m, _ := v.(map[string]any)
	return m
}

// str renders a JSON scalar as a trimmed string; null and objects are "".
func str(v any) string {
	switch x := v.(type) {
	case string:
		return strings.TrimSpace(x)
	case float64:
		return strconv.FormatInt(int64(x), 10)
	case bool:
		return strconv.FormatBool(x)
	}
	return ""
}

// asInt reads an integer that may arrive as a number or a numeric string
// (the plain template sends strings); anything else is nil.
func asInt(v any) *int {
	var n int
	switch x := v.(type) {
	case float64:
		n = int(x)
	case string:
		i, err := strconv.Atoi(strings.TrimSpace(x))
		if err != nil {
			return nil
		}
		n = i
	default:
		return nil
	}
	return &n
}

func asBool(v any) bool {
	switch x := v.(type) {
	case string:
		return strings.EqualFold(strings.TrimSpace(x), "true")
	case bool:
		return x
	}
	return false
}
