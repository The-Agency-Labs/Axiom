// Package showconv converts the shows tracked from TMDB to TheTVDB: it finds
// each show's TVDB series, moves every entry, watch, list item and note onto
// it and re-places each watch on the TVDB episode that is the same episode.
// TMDB lumps many shows into one long season; TVDB has the real aired seasons.
package showconv

import (
	"sort"
	"strings"
	"time"
	"unicode"
)

// Ep is one episode of either side of the alignment.
type Ep struct {
	Season, Number int
	Title          string
	Air            string // YYYY-MM-DD, "" when unknown
	ExtID          string // TVDB episode id (target side only)
}

// Key identifies an episode by season and number.
type Key [2]int

// Link says which target episode a source episode is, and how we know.
type Link struct {
	To     Key
	Method string
}

// Alignment methods, from the surest to the weakest.
const (
	MethodExact     = "exact"      // the two databases link the episodes by id
	MethodDate      = "date"       // the only episode of its class aired that day
	MethodDateOrder = "date+order" // several episodes aired that day: matched in order
	MethodDateNear  = "date±1"     // aired a day apart (time zones), unique
	MethodTitle     = "title"      // identical title, unique
	MethodOrder     = "order"      // placed by position between matched neighbours
)

// Linker returns the target episode id TMDB records for a source episode ("" when unknown).
type Linker func(season, number int) string

// Align matches the episodes of src (sorted by season, number) with those of
// dst (sorted likewise) one to one: by air date (episodes of one day in
// order), by a day's difference, by title, then by position between already
// matched neighbours. Regular episodes (season > 0) and specials (season 0)
// are matched within their own class. link, when not nil, is asked only for
// episodes the dates cannot settle (a day with a different number of
// episodes on each side) and for episodes still unmatched at the end; need
// tells whether an episode is worth a link lookup (it was watched).
func Align(src, dst []Ep, need func(Key) bool, link Linker) map[Key]Link {
	out := map[Key]Link{}
	used := make([]bool, len(dst))
	matchedSrc := make([]int, len(src)) // dst index + 1, 0 = none
	dstByKey := map[Key]int{}
	dstByExt := map[string]int{}
	for i, e := range dst {
		dstByKey[Key{e.Season, e.Number}] = i
		if e.ExtID != "" {
			dstByExt[e.ExtID] = i
		}
	}
	class := func(season int) int {
		if season == 0 {
			return 0
		}
		return 1
	}
	set := func(si, di int, method string) {
		used[di] = true
		matchedSrc[si] = di + 1
		out[Key{src[si].Season, src[si].Number}] = Link{To: Key{dst[di].Season, dst[di].Number}, Method: method}
	}
	tryLink := func(si int) bool {
		if link == nil || matchedSrc[si] != 0 {
			return false
		}
		k := Key{src[si].Season, src[si].Number}
		if need != nil && !need(k) {
			return false
		}
		id := link(k[0], k[1])
		if id == "" {
			return false
		}
		if di, ok := dstByExt[id]; ok && !used[di] {
			set(si, di, MethodExact)
			return true
		}
		return false
	}

	// Pass 1: by air date, per day and class.
	type dayKey struct {
		date  string
		class int
	}
	srcDays := map[dayKey][]int{}
	dstDays := map[dayKey][]int{}
	var dayOrder []dayKey
	for i, e := range src {
		if e.Air == "" {
			continue
		}
		k := dayKey{e.Air, class(e.Season)}
		if _, ok := srcDays[k]; !ok {
			dayOrder = append(dayOrder, k)
		}
		srcDays[k] = append(srcDays[k], i)
	}
	for i, e := range dst {
		if e.Air != "" {
			k := dayKey{e.Air, class(e.Season)}
			dstDays[k] = append(dstDays[k], i)
		}
	}
	for _, k := range dayOrder {
		ss, ds := srcDays[k], dstDays[k]
		switch {
		case len(ss) == 1 && len(ds) == 1:
			set(ss[0], ds[0], MethodDate)
		case len(ds) == 0:
		case len(ss) == len(ds):
			for j := range ss {
				set(ss[j], ds[j], MethodDateOrder)
			}
		default:
			// A day with a different number of episodes on each side: ids
			// first, then identical titles, then order for what is left.
			for _, si := range ss {
				tryLink(si)
			}
			for _, si := range ss {
				if matchedSrc[si] != 0 || generic(src[si].Title) {
					continue
				}
				var only = -1
				n := 0
				for _, di := range ds {
					if !used[di] && sameTitle(src[si].Title, dst[di].Title) {
						only, n = di, n+1
					}
				}
				if n == 1 {
					set(si, only, MethodTitle)
				}
			}
			var free []int
			for _, di := range ds {
				if !used[di] {
					free = append(free, di)
				}
			}
			for _, si := range ss {
				if matchedSrc[si] == 0 && len(free) > 0 {
					set(si, free[0], MethodDateOrder)
					free = free[1:]
				}
			}
		}
	}

	// Pass 2: a day apart (the two databases disagree on time zones).
	for si, e := range src {
		if matchedSrc[si] != 0 || e.Air == "" {
			continue
		}
		var only, n = -1, 0
		for _, off := range []int{-1, 1} {
			d := shiftDay(e.Air, off)
			for _, di := range dstDays[dayKey{d, class(e.Season)}] {
				if !used[di] {
					only, n = di, n+1
				}
			}
		}
		if n == 1 {
			set(si, only, MethodDateNear)
		}
	}

	// Pass 3: identical titles, unique on the free target episodes.
	titleIdx := map[string][]int{}
	for di, e := range dst {
		if !used[di] && !generic(e.Title) {
			n := normalize(e.Title)
			titleIdx[n] = append(titleIdx[n], di)
		}
	}
	for si, e := range src {
		if matchedSrc[si] != 0 || generic(e.Title) {
			continue
		}
		c := titleIdx[normalize(e.Title)]
		var free []int
		for _, di := range c {
			if !used[di] && class(dst[di].Season) == class(e.Season) {
				free = append(free, di)
			}
		}
		if len(free) == 1 {
			set(si, free[0], MethodTitle)
		}
	}

	// Pass 4: regular episodes still open take the place their matched
	// neighbours leave them (episode i after the last matched one is the
	// target episode just after that one's twin).
	var srcReg, dstReg []int
	for i, e := range src {
		if e.Season > 0 {
			srcReg = append(srcReg, i)
		}
	}
	for i, e := range dst {
		if e.Season > 0 {
			dstReg = append(dstReg, i)
		}
	}
	dstPos := map[int]int{} // dst index -> position among regular
	for p, di := range dstReg {
		dstPos[di] = p
	}
	srcAnchor := make([]int, len(srcReg)) // dst regular position + 1
	for p, si := range srcReg {
		if m := matchedSrc[si]; m != 0 {
			if dp, ok := dstPos[m-1]; ok {
				srcAnchor[p] = dp + 1
			}
		}
	}
	for p, si := range srcReg {
		if matchedSrc[si] != 0 {
			continue
		}
		prev, next := -1, -1
		for q := p - 1; q >= 0; q-- {
			if srcAnchor[q] != 0 {
				prev = q
				break
			}
		}
		for q := p + 1; q < len(srcReg); q++ {
			if srcAnchor[q] != 0 {
				next = q
				break
			}
		}
		var target int
		switch {
		case prev >= 0:
			target = srcAnchor[prev] - 1 + (p - prev)
		case next >= 0:
			target = srcAnchor[next] - 1 - (next - p)
		default:
			target = p
		}
		if target < 0 || target >= len(dstReg) {
			continue
		}
		if next >= 0 && target >= srcAnchor[next]-1 {
			continue
		}
		if prev >= 0 && target <= srcAnchor[prev]-1 {
			continue
		}
		di := dstReg[target]
		if used[di] || farApart(src[si].Air, dst[di].Air) {
			continue
		}
		set(si, di, MethodOrder)
		srcAnchor[p] = target + 1
	}

	// Pass 5: ids for whatever is left and worth asking about.
	for si := range src {
		tryLink(si)
	}
	return out
}

// farApart reports two known air dates more than two weeks apart.
func farApart(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	ta, err1 := time.Parse(time.DateOnly, a)
	tb, err2 := time.Parse(time.DateOnly, b)
	if err1 != nil || err2 != nil {
		return false
	}
	d := ta.Sub(tb)
	if d < 0 {
		d = -d
	}
	return d > 14*24*time.Hour
}

func shiftDay(date string, days int) string {
	t, err := time.Parse(time.DateOnly, date)
	if err != nil {
		return ""
	}
	return t.AddDate(0, 0, days).Format(time.DateOnly)
}

// generic reports a placeholder title ("Episode 5", "") that says nothing.
func generic(title string) bool {
	n := normalize(title)
	if n == "" {
		return true
	}
	return strings.HasPrefix(n, "episode") && strings.Trim(n[len("episode"):], "0123456789") == ""
}

func sameTitle(a, b string) bool {
	na, nb := normalize(a), normalize(b)
	return na != "" && na == nb
}

func normalize(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// closest picks the target episode for a watch the alignment has no twin
// for: the regular (or special) episode aired nearest to the source
// episode's air date; without a date, the same number in the same season; else
// the nearest number of the nearest season. ok is false when dst is empty.
func closest(season, number int, air string, dst []Ep) (Key, string, bool) {
	if len(dst) == 0 {
		return Key{}, "", false
	}
	same := func(e Ep) bool { return (e.Season == 0) == (season == 0) }
	if t, err := time.Parse(time.DateOnly, air); err == nil {
		best, bestD := -1, time.Duration(1<<62)
		for i, e := range dst {
			if !same(e) || e.Air == "" {
				continue
			}
			te, err := time.Parse(time.DateOnly, e.Air)
			if err != nil {
				continue
			}
			d := te.Sub(t)
			if d < 0 {
				d = -d
			}
			if d < bestD {
				best, bestD = i, d
			}
		}
		if best >= 0 {
			return Key{dst[best].Season, dst[best].Number}, "nearest air date", true
		}
	}
	for _, e := range dst {
		if e.Season == season && e.Number == number {
			return Key{season, number}, "same number", true
		}
	}
	// Nearest by season, then by number.
	seasons := map[int]bool{}
	for _, e := range dst {
		seasons[e.Season] = true
	}
	var list []int
	for s := range seasons {
		list = append(list, s)
	}
	sort.Ints(list)
	bestS := list[0]
	for _, s := range list {
		if abs(s-season) < abs(bestS-season) {
			bestS = s
		}
	}
	var pick Ep
	first := true
	for _, e := range dst {
		if e.Season != bestS {
			continue
		}
		if first || abs(e.Number-number) < abs(pick.Number-number) {
			pick, first = e, false
		}
	}
	return Key{pick.Season, pick.Number}, "nearest number", true
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}
