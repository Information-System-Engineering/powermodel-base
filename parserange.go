package keplerpowermodel

import (
	"fmt"
	"strings"
	"time"
)

// ParseRange reads a replay window written as RFC3339..RFC3339.
//
// cmd/strip writes exactly this form to span.txt, so the common invocation is
// -range "$(cat <snapshot>/span.txt)" and nobody has to guess a window.
func ParseRange(s string) (time.Time, time.Time, error) {
	a, b, found := strings.Cut(s, "..")
	if !found {
		return time.Time{}, time.Time{}, fmt.Errorf("range %q: want RFC3339..RFC3339", s)
	}
	start, err := time.Parse(time.RFC3339, a)
	if err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("range start: %w", err)
	}
	end, err := time.Parse(time.RFC3339, b)
	if err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("range end: %w", err)
	}
	if !end.After(start) {
		return time.Time{}, time.Time{}, fmt.Errorf("range end must be after start")
	}
	return start, end, nil
}
