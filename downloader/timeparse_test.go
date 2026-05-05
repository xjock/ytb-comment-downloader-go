package downloader

import (
	"testing"
	"time"
)

func TestParseRelativeTime(t *testing.T) {
	now := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)

	cases := []struct {
		in   string
		want time.Time
	}{
		{"5 seconds ago", now.Add(-5 * time.Second)},
		{"1 minute ago", now.Add(-time.Minute)},
		{"3 hours ago", now.Add(-3 * time.Hour)},
		{"2 days ago", now.AddDate(0, 0, -2)},
		{"1 week ago", now.AddDate(0, 0, -7)},
		{"4 weeks ago", now.AddDate(0, 0, -28)},
		{"6 months ago", now.AddDate(0, -6, 0)},
		{"1 year ago", now.AddDate(-1, 0, 0)},
		{"2 days ago (edited)", now.AddDate(0, 0, -2)},
		{"1,234 days ago", now.AddDate(0, 0, -1234)},
	}
	for _, c := range cases {
		got, err := ParseRelativeTime(c.in, now)
		if err != nil {
			t.Errorf("%q: unexpected error: %v", c.in, err)
			continue
		}
		if !got.Equal(c.want) {
			t.Errorf("%q: got %v want %v", c.in, got, c.want)
		}
	}
}

func TestParseRelativeTime_Absolute(t *testing.T) {
	now := time.Date(2026, 5, 5, 0, 0, 0, 0, time.UTC)
	got, err := ParseRelativeTime("Jan 5, 2020", now)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := time.Date(2020, 1, 5, 0, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Errorf("got %v want %v", got, want)
	}
}

func TestParseRelativeTime_Unparsed(t *testing.T) {
	if _, err := ParseRelativeTime("yesterday", time.Now()); err == nil {
		t.Fatal("expected error for unrecognised input")
	}
	if _, err := ParseRelativeTime("", time.Now()); err == nil {
		t.Fatal("expected error for empty input")
	}
}
