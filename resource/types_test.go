package resource

import (
	"testing"
	"time"
)

func TestStepClockAdvances(t *testing.T) {
	start := time.Unix(1788948000, 0)
	c := NewStepClock(start, 10*time.Second)
	if !c.Now().Equal(start) {
		t.Fatalf("a fresh clock must sit at start: %v", c.Now())
	}
	if got := c.Advance(); !got.Equal(start.Add(10 * time.Second)) {
		t.Fatalf("Advance: got %v", got)
	}
	if !c.Now().Equal(start.Add(10 * time.Second)) {
		t.Fatalf("Now must follow Advance: %v", c.Now())
	}
}

func TestWallClockMoves(t *testing.T) {
	if WallClock().Now().IsZero() {
		t.Fatal("wall clock returned the zero time")
	}
}
