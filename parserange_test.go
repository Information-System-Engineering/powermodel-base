package keplerpowermodel

import (
	"testing"
	"time"
)

func TestParseRange(t *testing.T) {
	start, end, err := ParseRange("2026-09-09T10:00:00Z..2026-09-09T11:00:00Z")
	if err != nil {
		t.Fatal(err)
	}
	if !start.Equal(time.Date(2026, 9, 9, 10, 0, 0, 0, time.UTC)) {
		t.Errorf("start = %v", start)
	}
	if !end.Equal(time.Date(2026, 9, 9, 11, 0, 0, 0, time.UTC)) {
		t.Errorf("end = %v", end)
	}

	for _, bad := range []string{
		"",
		"nonsense",
		"2026-09-09T10:00:00Z",
		"2026-09-09T10:00:00Z..nonsense",
		// A backwards window yields zero steps a thousand lines later; say so now.
		"2026-09-09T11:00:00Z..2026-09-09T10:00:00Z",
	} {
		if _, _, err := ParseRange(bad); err == nil {
			t.Errorf("ParseRange(%q) = nil error, want one", bad)
		}
	}
}
