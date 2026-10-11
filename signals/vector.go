package signals

// Sum totals every sample's value. An empty or nil Vector sums to zero: a
// signal that matched nothing at this step is a gap, and a gap is not an error.
func (v Vector) Sum() float64 {
	total := 0.0
	for _, s := range v {
		total += s.Value
	}
	return total
}

// First returns the first sample's value, or zero for an empty Vector.
// Use it for signals you expect to be single-series, like a node-level gauge.
func (v Vector) First() float64 {
	if len(v) == 0 {
		return 0
	}
	return v[0].Value
}
