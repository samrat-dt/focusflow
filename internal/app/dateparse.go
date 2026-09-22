package app

import (
	"fmt"
	"strings"
	"time"
)

// parseDueDate converts a human-friendly date expression to a time.Time.
// Accepts: YYYY-MM-DD, today, tomorrow, weekday names (next occurrence),
// Nd (N days from now). `now` is passed in for testability.
func parseDueDate(s string, now time.Time) (time.Time, error) {
	s = strings.TrimSpace(strings.ToLower(s))

	switch s {
	case "today":
		y, m, d := now.Date()
		return time.Date(y, m, d, 0, 0, 0, 0, now.Location()), nil
	case "tomorrow":
		y, m, d := now.AddDate(0, 0, 1).Date()
		return time.Date(y, m, d, 0, 0, 0, 0, now.Location()), nil
	}

	// Weekday names — next occurrence (today if it matches).
	weekdays := map[string]time.Weekday{
		"sunday": time.Sunday, "monday": time.Monday,
		"tuesday": time.Tuesday, "wednesday": time.Wednesday,
		"thursday": time.Thursday, "friday": time.Friday,
		"saturday": time.Saturday,
	}
	if target, ok := weekdays[s]; ok {
		current := now.Weekday()
		delta := int(target) - int(current)
		if delta <= 0 {
			delta += 7
		}
		d := now.AddDate(0, 0, delta)
		y, m, dd := d.Date()
		return time.Date(y, m, dd, 0, 0, 0, 0, now.Location()), nil
	}

	// Pattern "Nd" — N days from now.
	if strings.HasSuffix(s, "d") {
		numStr := strings.TrimSuffix(s, "d")
		var n int
		if _, err := fmt.Sscanf(numStr, "%d", &n); err == nil && n >= 0 {
			d := now.AddDate(0, 0, n)
			y, m, dd := d.Date()
			return time.Date(y, m, dd, 0, 0, 0, 0, now.Location()), nil
		}
	}

	// YYYY-MM-DD.
	if t, err := time.ParseInLocation("2006-01-02", s, now.Location()); err == nil {
		return t, nil
	}

	return time.Time{}, fmt.Errorf("unrecognized date %q (want YYYY-MM-DD, today, tomorrow, monday..sunday, Nd)", s)
}
