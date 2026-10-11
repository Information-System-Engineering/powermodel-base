package main

import (
	"fmt"
	"math"
	"slices"
)

type Pred struct {
	TS   int64
	Node string
	Zone string
	W    float64
	Pods float64 // sum of pred_pod_w for this row; 0 when the column is empty
}

type Truth struct {
	TS   int64
	Node string
	Zone string
	W    float64
}

type ZoneStats struct {
	Zone      string
	N         int     // matched points: truth with a finite prediction
	Coverage  float64 // percent of truth points matched
	MAE       float64 // over matched points; NaN when none
	RMSE      float64
	MAPE      float64 // NaN when every matched point has zero truth; +Inf when none matched
	EnergyErr float64 // percent, over the full truth span, a miss counted as 0 W
}

// PredNode returns the single node the predictions describe.
//
// The model is per node: a replay runs with one -node, so predictions carry
// exactly one. More than one means the file was concatenated from separate
// runs, which would silently average two machines together.
func PredNode(pred []Pred) (string, error) {
	seen := map[string]bool{}
	for _, p := range pred {
		seen[p.Node] = true
	}
	switch len(seen) {
	case 0:
		return "", fmt.Errorf("predictions are empty")
	case 1:
		for n := range seen {
			return n, nil
		}
	}
	return "", fmt.Errorf("predictions carry %d nodes (%v): the model is per node, "+
		"so score one run at a time", len(seen), sortedKeysOf(seen))
}

// FilterTruth keeps only the rows measured on the given node.
//
// Without this the join key (ts, zone) matches every node's truth at that
// timestamp. A two-node snapshot would score one node's estimate against
// another node's meter -- and with a map-based join it would do so silently,
// with an n that looks entirely normal.
func FilterTruth(truth []Truth, node string) []Truth {
	out := make([]Truth, 0, len(truth))
	for _, t := range truth {
		if t.Node == node {
			out = append(out, t)
		}
	}
	return out
}

// ClipTruth keeps the truth inside (start, end]: the window a replay over
// span.txt scores. The start is excluded because the replay drops its first
// step.
func ClipTruth(truth []Truth, start, end int64) []Truth {
	out := make([]Truth, 0, len(truth))
	for _, t := range truth {
		if t.TS > start && t.TS <= end {
			out = append(out, t)
		}
	}
	return out
}

// AlignTruth keeps the truth on the prediction grid and returns both steps.
//
// The prediction grid must be the truth grid or a coarser multiple of it,
// starting on a truth timestamp. Anything else (a 15 s step against 10 s
// truth, a range starting off span.txt's grid, one stray row) is an error,
// not a silent coverage loss. Truth between prediction steps is dropped, so a
// 20 s run is scored on the 20 s points it claimed, not charged for the rest.
func AlignTruth(pred []Pred, truth []Truth) ([]Truth, int64, int64, error) {
	tts := make([]int64, len(truth))
	for i, t := range truth {
		tts[i] = t.TS
	}
	tOrigin, tStep := grid(tts)
	if tStep == 0 {
		return nil, 0, 0, fmt.Errorf("truth has fewer than two timestamps: no step to align to")
	}
	pts := make([]int64, len(pred))
	for i, p := range pred {
		pts[i] = p.TS
	}
	pOrigin, pStep := grid(pts)
	if pStep == 0 {
		pStep = tStep // a single timestamp: nothing to be off-grid against
	}
	if (pOrigin-tOrigin)%tStep != 0 || pStep%tStep != 0 {
		return nil, 0, 0, fmt.Errorf("predictions sit on a %d s grid from ts %d, truth on a %d s grid from ts %d: "+
			"use a --step that is a multiple of %d s and a --range start on the truth grid (span.txt's start is)",
			pStep, pOrigin, tStep, tOrigin, tStep)
	}
	out := make([]Truth, 0, len(truth))
	for _, t := range truth {
		if (t.TS-pOrigin)%pStep == 0 {
			out = append(out, t)
		}
	}
	return out, pStep, tStep, nil
}

// grid returns the earliest timestamp and the coarsest step every timestamp
// lies on: the GCD of their distances from the earliest. Step 0 means fewer
// than two distinct timestamps.
func grid(ts []int64) (origin, step int64) {
	if len(ts) == 0 {
		return 0, 0
	}
	origin = slices.Min(ts)
	for _, t := range ts {
		a, b := step, t-origin
		for b != 0 {
			a, b = b, a%b
		}
		step = a
	}
	return origin, step
}

// TruthNodes lists the nodes a truth file covers, for error messages.
func TruthNodes(truth []Truth) []string {
	seen := map[string]bool{}
	for _, t := range truth {
		seen[t.Node] = true
	}
	return sortedKeysOf(seen)
}

func sortedKeysOf(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

// Score walks the truth, not the predictions. Every truth point is either
// matched by a finite prediction or a miss. Misses count toward coverage and
// as 0 W in energy error, so skipping hard steps or whole zones cannot score
// well.
func Score(pred []Pred, truth []Truth) []ZoneStats {
	type key struct {
		ts   int64
		zone string
	}
	pw := make(map[key]float64, len(pred))
	for _, p := range pred {
		pw[key{p.TS, p.Zone}] = p.W
	}

	type acc struct {
		ts                []int64
		total, n, mapeN   int
		absErr, sqErr     float64
		mapeSum           float64
		sumPred, sumTruth float64
	}
	byZone := map[string]*acc{}
	for _, t := range truth {
		a := byZone[t.Zone]
		if a == nil {
			a = &acc{}
			byZone[t.Zone] = a
		}
		a.ts = append(a.ts, t.TS)
		a.total++
		a.sumTruth += t.W
		w, ok := pw[key{t.TS, t.Zone}]
		if !ok || math.IsNaN(w) || math.IsInf(w, 0) {
			continue
		}
		err := w - t.W
		a.absErr += math.Abs(err)
		a.sqErr += err * err
		a.n++
		a.sumPred += w
		// MAPE is undefined where truth is zero; excluding those points is
		// honest, silently treating them as zero error is not.
		if t.W != 0 {
			a.mapeSum += math.Abs(err) / math.Abs(t.W)
			a.mapeN++
		}
	}

	zones := make([]string, 0, len(byZone))
	for z := range byZone {
		zones = append(zones, z)
	}
	slices.Sort(zones)

	out := make([]ZoneStats, 0, len(zones))
	for _, z := range zones {
		a := byZone[z]
		s := ZoneStats{Zone: z, N: a.n,
			Coverage: float64(a.n) / float64(a.total) * 100,
			MAE:      math.NaN(), RMSE: math.NaN(), MAPE: math.NaN(),
		}
		if a.n > 0 {
			s.MAE = a.absErr / float64(a.n)
			s.RMSE = math.Sqrt(a.sqErr / float64(a.n))
		} else {
			// Never predicted: the worst possible zone, not an absent one.
			s.MAPE = math.Inf(1)
		}
		if a.mapeN > 0 {
			s.MAPE = a.mapeSum / float64(a.mapeN) * 100
		}
		step := medianStep(a.ts)
		if et := a.sumTruth * step; et != 0 {
			s.EnergyErr = (a.sumPred*step - et) / et * 100
		} else {
			s.EnergyErr = math.NaN()
		}
		out = append(out, s)
	}
	return out
}

type PodCheck struct {
	Zone        string
	PodJ, NodeJ float64
	NA          bool // node measured no energy: no ratio to report
}

// CheckPods sums, per truth zone over the full truth span, the predicted pod
// energy next to the measured node energy. Pod watts are read from
// prediction rows at truth timestamps; a row without them adds nothing.
func CheckPods(pred []Pred, truth []Truth) []PodCheck {
	type key struct {
		ts   int64
		zone string
	}
	pods := make(map[key]float64, len(pred))
	for _, p := range pred {
		pods[key{p.TS, p.Zone}] = p.Pods
	}
	type acc struct {
		ts         []int64
		node, pods float64
	}
	byZone := map[string]*acc{}
	for _, t := range truth {
		a := byZone[t.Zone]
		if a == nil {
			a = &acc{}
			byZone[t.Zone] = a
		}
		a.ts = append(a.ts, t.TS)
		a.node += t.W
		a.pods += pods[key{t.TS, t.Zone}]
	}
	zones := make([]string, 0, len(byZone))
	for z := range byZone {
		zones = append(zones, z)
	}
	slices.Sort(zones)
	out := make([]PodCheck, 0, len(zones))
	for _, z := range zones {
		a := byZone[z]
		step := medianStep(a.ts)
		c := PodCheck{Zone: z, PodJ: a.pods * step, NodeJ: a.node * step}
		// A zone measuring 0 J (an unpowered uncore, say) has no ratio; MAPE
		// treats the same zone as undefined.
		c.NA = !(c.NodeJ > 0)
		out = append(out, c)
	}
	return out
}

// medianStep infers the sample interval from the timestamps, falling back to
// 10s when there is no usable interval. Energy is a sum times a step, so
// guessing the step wrong scales every energy number.
//
// The differences are consecutive in join order, not sorted, and an even
// count takes the mean of the middle two.
func medianStep(ts []int64) float64 {
	if len(ts) < 2 {
		return 10
	}
	diffs := make([]float64, 0, len(ts)-1)
	for i := 1; i < len(ts); i++ {
		diffs = append(diffs, float64(ts[i]-ts[i-1]))
	}
	slices.Sort(diffs)
	m := diffs[len(diffs)/2]
	if len(diffs)%2 == 0 {
		m = (diffs[len(diffs)/2-1] + m) / 2
	}
	if m > 0 {
		return m
	}
	return 10
}
