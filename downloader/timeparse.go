package downloader

import (
	"errors"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// ErrUnparsedTime is returned when ParseRelativeTime cannot interpret the
// input. Callers typically swallow it and leave time_parsed unset, matching
// the Python `dateparser` behaviour where it returns None.
var ErrUnparsedTime = errors.New("downloader: unrecognised time format")

var relativeTimeRe = regexp.MustCompile(`(?i)^([\d,]+)\s+(second|minute|hour|day|week|month|year)s?\s+ago\b`)

// ParseRelativeTime accepts strings YouTube uses for comment timestamps
// (e.g. "2 days ago", "5 months ago", "1 year ago (edited)") and returns the
// approximate absolute time relative to `now`.
//
// It also accepts a handful of absolute formats YouTube falls back to for
// older comments. Returns ErrUnparsedTime when nothing matches.
func ParseRelativeTime(s string, now time.Time) (time.Time, error) {
	// Match Python: drop a trailing parenthetical such as "(edited)".
	if i := strings.Index(s, "("); i >= 0 {
		s = s[:i]
	}
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, ErrUnparsedTime
	}

	if m := relativeTimeRe.FindStringSubmatch(s); m != nil {
		nStr := strings.ReplaceAll(m[1], ",", "")
		n, err := strconv.Atoi(nStr)
		if err != nil {
			return time.Time{}, ErrUnparsedTime
		}
		switch strings.ToLower(m[2]) {
		case "second":
			return now.Add(-time.Duration(n) * time.Second), nil
		case "minute":
			return now.Add(-time.Duration(n) * time.Minute), nil
		case "hour":
			return now.Add(-time.Duration(n) * time.Hour), nil
		case "day":
			return now.AddDate(0, 0, -n), nil
		case "week":
			return now.AddDate(0, 0, -7*n), nil
		case "month":
			return now.AddDate(0, -n, 0), nil
		case "year":
			return now.AddDate(-n, 0, 0), nil
		}
	}

	// Absolute fallbacks. YouTube occasionally surfaces these for very old
	// comments or when the UI language flips locale.
	for _, layout := range []string{
		"Jan 2, 2006",
		"January 2, 2006",
		"2 Jan 2006",
		"2006-01-02",
	} {
		if t, err := time.ParseInLocation(layout, s, now.Location()); err == nil {
			return t, nil
		}
	}

	return time.Time{}, ErrUnparsedTime
}
