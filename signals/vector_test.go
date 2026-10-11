package signals

import "testing"

func TestVectorSum(t *testing.T) {
	v := Vector{{Value: 1.5}, {Value: 2.0}, {Value: -0.5}}
	if got := v.Sum(); got != 3.0 {
		t.Errorf("Sum() = %v, want 3", got)
	}
	// An empty vector is a signal that matched nothing at this step, which is
	// a gap, not an error. Callers decide what a gap means; Sum reports zero.
	if got := (Vector{}).Sum(); got != 0 {
		t.Errorf("empty Sum() = %v, want 0", got)
	}
	if got := Vector(nil).Sum(); got != 0 {
		t.Errorf("nil Sum() = %v, want 0", got)
	}
}

func TestVectorFirst(t *testing.T) {
	v := Vector{{Value: 7.0}, {Value: 9.0}}
	if got := v.First(); got != 7.0 {
		t.Errorf("First() = %v, want 7", got)
	}
	if got := (Vector{}).First(); got != 0 {
		t.Errorf("empty First() = %v, want 0", got)
	}
}
