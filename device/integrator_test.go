package device

import (
	"testing"
	"time"
)

func TestIntegratorAccumulates(t *testing.T) {
	i := NewIntegrator(1000 * Joule)
	// 10 W for 2 s = 20 J
	if got := i.Add(10, 2*time.Second); got != 20*Joule {
		t.Fatalf("first Add: got %d, want %d", got, 20*Joule)
	}
	// counters climb, they do not reset
	if got := i.Add(10, 2*time.Second); got != 40*Joule {
		t.Fatalf("second Add: got %d, want %d", got, 40*Joule)
	}
}

func TestIntegratorWrapsAtMax(t *testing.T) {
	i := NewIntegrator(30 * Joule)
	i.Add(10, 2*time.Second) // 20 J
	// 20 + 20 = 40, wraps past 30 to 10
	if got := i.Add(10, 2*time.Second); got != 10*Joule {
		t.Fatalf("wrap: got %d, want %d", got, 10*Joule)
	}
}

func TestIntegratorIgnoresNegativePower(t *testing.T) {
	i := NewIntegrator(1000 * Joule)
	if got := i.Add(-5, time.Second); got != 0 {
		t.Fatalf("negative power must not decrement the counter: got %d", got)
	}
}
