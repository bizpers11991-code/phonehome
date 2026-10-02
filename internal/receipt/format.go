package receipt

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// clean makes s printable on the grid: control characters become spaces,
// runs of space collapse, and runes Go Mono cannot draw become '?'.
func clean(s string) string {
	var b strings.Builder
	space := false
	for _, r := range s {
		switch {
		case unicode.IsSpace(r) || unicode.IsControl(r):
			space = b.Len() > 0
			continue
		case !has(r):
			r = '?'
		}
		if space {
			b.WriteByte(' ')
			space = false
		}
		b.WriteRune(r)
	}
	return b.String()
}

func width(s string) int { return utf8.RuneCountInString(s) }

// fit truncates s to n cells, ending in an ellipsis when it had to cut.
func fit(s string, n int) string {
	if width(s) <= n {
		return s
	}
	if n <= 0 {
		return ""
	}
	r := []rune(s)
	return strings.TrimRight(string(r[:n-1]), " ") + "…"
}

// wrap breaks s into lines of at most n cells, splitting words that are
// longer than a line.
func wrap(s string, n int) []string {
	var lines []string
	var cur []rune
	for _, w := range strings.Fields(s) {
		word := []rune(w)
		if len(cur) > 0 && len(cur)+1+len(word) > n {
			lines = append(lines, string(cur))
			cur = nil
		}
		for len(word) > n {
			if len(cur) > 0 {
				lines = append(lines, string(cur))
				cur = nil
			}
			lines = append(lines, string(word[:n]))
			word = word[n:]
		}
		if len(cur) > 0 {
			cur = append(cur, ' ')
		}
		cur = append(cur, word...)
	}
	if len(cur) > 0 {
		lines = append(lines, string(cur))
	}
	return lines
}

func center(s string, n int) string {
	s = fit(s, n)
	return strings.Repeat(" ", (n-width(s))/2) + s
}

// leader joins key and val with a dotted leader so val ends at column n:
// "CONTENT RECOGNITION .......  9,812". Two spaces keep the last dot clear of
// the value (Go Mono's "1" has a wide flag); the key is truncated rather than
// ever letting the dots touch either side.
func leader(key, val string, n int) string {
	const gaps = 3 // one space after the key, two before the value
	val = fit(val, n-gaps-2)
	key = fit(key, n-width(val)-gaps-2)
	dots := n - width(key) - width(val) - gaps
	return key + " " + strings.Repeat(".", dots) + "  " + val
}

// thousands formats n with comma separators.
func thousands(n int) string {
	s := strconv.Itoa(n)
	neg := strings.HasPrefix(s, "-")
	s = strings.TrimPrefix(s, "-")
	var b strings.Builder
	for i, r := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(r)
	}
	if neg {
		return "-" + b.String()
	}
	return b.String()
}

// rate formats a per-day figure: whole numbers above 10, one decimal below.
func rate(f float64) string {
	if f < 10 && math.Round(f) != f {
		return strconv.FormatFloat(f, 'f', 1, 64)
	}
	return thousands(int(math.Round(f)))
}

func percent(f float64) string {
	return strconv.Itoa(int(math.Round(f*100))) + "%"
}

// every humanises an interval: "every 15s", "every 4 min", "every 2 h".
func every(d time.Duration) string {
	switch s := d.Seconds(); {
	case s < 59.5:
		return fmt.Sprintf("every %ds", max(1, int(math.Round(s))))
	case s < 90*60:
		return fmt.Sprintf("every %d min", int(math.Round(s/60)))
	case s < 36*3600:
		return fmt.Sprintf("every %d h", int(math.Round(s/3600)))
	default:
		return fmt.Sprintf("every %d days", int(math.Round(s/86400)))
	}
}

// period formats a half-open range in loc: "Sep 25 – Oct 2, 2026".
func period(from, to time.Time, loc *time.Location) string {
	from = from.In(loc)
	last := to.In(loc)
	if to.After(from) {
		last = to.Add(-time.Nanosecond).In(loc)
	}
	switch {
	case from.Year() != last.Year():
		return from.Format("Jan 2, 2006") + " – " + last.Format("Jan 2, 2006")
	case from.YearDay() == last.YearDay():
		return from.Format("Jan 2, 2006")
	}
	return from.Format("Jan 2") + " – " + last.Format("Jan 2, 2006")
}
