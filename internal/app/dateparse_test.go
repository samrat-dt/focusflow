package app

import (
	"testing"
	"time"
)

func TestParseDueDate(t *testing.T) {
	// Fixed "now": Wednesday 2026-04-15 14:00 UTC.
	now := time.Date(2026, 4, 15, 14, 0, 0, 0, time.UTC)

	cases := []struct {
		in      string
		want    string // YYYY-MM-DD
		wantErr bool
	}{
		{"today", "2026-04-15", false},
		{"tomorrow", "2026-04-16", false},
		{"monday", "2026-04-20", false},    // next Monday (Wed→Mon = +5)
		{"wednesday", "2026-04-22", false}, // next Wednesday (not today)
		{"friday", "2026-04-17", false},    // next Friday = +2
		{"3d", "2026-04-18", false},
		{"0d", "2026-04-15", false},
		{"2026-12-25", "2026-12-25", false},
		{"TODAY", "2026-04-15", false}, // case insensitive
		{"garble", "", true},
	}
	for _, c := range cases {
		got, err := parseDueDate(c.in, now)
		if c.wantErr {
			if err == nil {
				t.Errorf("parseDueDate(%q): expected error", c.in)
			}
			continue
		}
		if err != nil {
			t.Errorf("parseDueDate(%q): unexpected error %v", c.in, err)
			continue
		}
		gotStr := got.Format("2006-01-02")
		if gotStr != c.want {
			t.Errorf("parseDueDate(%q): got %s, want %s", c.in, gotStr, c.want)
		}
	}
}
