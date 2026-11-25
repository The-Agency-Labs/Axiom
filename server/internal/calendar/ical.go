// Package calendar encodes the iCal (RFC 5545) feed. It has no dependencies:
// events are all-day (DATE values) or timed (UTC date-times), so no VTIMEZONE
// is needed.
package calendar

import (
	"bytes"
	"strings"
	"time"
	"unicode/utf8"
)

// Event is one VEVENT.
type Event struct {
	UID         string
	Summary     string
	Description string
	Start       time.Time // UTC; for all-day events only the date counts
	AllDay      bool
}

const (
	dateLayout     = "20060102"
	dateTimeLayout = "20060102T150405Z"
	maxLine        = 75 // octets per line, excluding the CRLF
)

// Encode renders a VCALENDAR. stamp is the DTSTAMP of every event.
func Encode(prodID, name string, stamp time.Time, events []Event) []byte {
	var b bytes.Buffer
	line := func(s string) { fold(&b, s) }
	line("BEGIN:VCALENDAR")
	line("VERSION:2.0")
	line("PRODID:" + prodID)
	line("CALSCALE:GREGORIAN")
	line("METHOD:PUBLISH")
	line("X-WR-CALNAME:" + Escape(name))
	for _, e := range events {
		line("BEGIN:VEVENT")
		line("UID:" + Escape(e.UID))
		line("DTSTAMP:" + stamp.UTC().Format(dateTimeLayout))
		if e.AllDay {
			d := e.Start.UTC().Truncate(24 * time.Hour)
			line("DTSTART;VALUE=DATE:" + d.Format(dateLayout))
			line("DTEND;VALUE=DATE:" + d.AddDate(0, 0, 1).Format(dateLayout))
		} else {
			line("DTSTART:" + e.Start.UTC().Format(dateTimeLayout))
			line("DTEND:" + e.Start.UTC().Format(dateTimeLayout))
		}
		line("SUMMARY:" + Escape(e.Summary))
		if e.Description != "" {
			line("DESCRIPTION:" + Escape(e.Description))
		}
		line("TRANSP:TRANSPARENT")
		line("END:VEVENT")
	}
	line("END:VCALENDAR")
	return b.Bytes()
}

// Escape escapes a TEXT value (RFC 5545 3.3.11): backslash, semicolon, comma
// and newlines. Other control characters are dropped.
func Escape(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		r, n := utf8.DecodeRuneInString(s[i:])
		i += n
		switch {
		case r == '\\':
			b.WriteString(`\\`)
		case r == ';':
			b.WriteString(`\;`)
		case r == ',':
			b.WriteString(`\,`)
		case r == '\n':
			b.WriteString(`\n`)
		case r == '\r':
			// folded into \n above (CRLF) or dropped (lone CR)
		case r < 0x20 && r != '\t', r == 0x7f, r == utf8.RuneError && n == 1:
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// fold writes one content line, folding at maxLine octets without splitting a
// UTF-8 sequence; continuation lines start with a single space.
func fold(b *bytes.Buffer, s string) {
	limit := maxLine
	for len(s) > limit {
		cut := limit
		for cut > 0 && !utf8.RuneStart(s[cut]) {
			cut--
		}
		b.WriteString(s[:cut])
		b.WriteString("\r\n ")
		s = s[cut:]
		limit = maxLine - 1
	}
	b.WriteString(s)
	b.WriteString("\r\n")
}
