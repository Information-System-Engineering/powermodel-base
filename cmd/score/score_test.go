package main

import (
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestScoreBasicStatistics(t *testing.T) {
	pred := []Pred{
		{TS: 0, Zone: "package", W: 90}, {TS: 10, Zone: "package", W: 110},
		{TS: 0, Zone: "dram", W: 10}, {TS: 10, Zone: "dram", W: 10},
	}
	truth := []Truth{
		{TS: 0, Zone: "package", W: 100}, {TS: 10, Zone: "package", W: 100},
		{TS: 0, Zone: "dram", W: 10}, {TS: 10, Zone: "dram", W: 10},
	}
	byZone := index(Score(pred, truth))

	if got := byZone["package"].MAE; got != 10 {
		t.Errorf("package MAE = %v, want 10", got)
	}
	if got := byZone["package"].MAPE; math.Abs(got-10) > 1e-9 {
		t.Errorf("package MAPE = %v, want 10", got)
	}
	// 90+110 = 200 predicted against 200 truth: energy over the span matches
	// even though every sample is wrong. That is exactly why energy error is
	// reported next to MAE rather than instead of it.
	if got := byZone["package"].EnergyErr; math.Abs(got) > 1e-9 {
		t.Errorf("package energy err = %v, want 0", got)
	}
	if got := byZone["dram"].MAE; got != 0 {
		t.Errorf("dram MAE = %v, want 0", got)
	}
}

func TestScoreZeroTruthGivesNaNMAPE(t *testing.T) {
	got := Score(
		[]Pred{{TS: 0, Zone: "package", W: 5}},
		[]Truth{{TS: 0, Zone: "package", W: 0}},
	)
	// MAPE is undefined where truth is zero. Excluding those points is honest;
	// silently treating them as zero error is not.
	if !math.IsNaN(got[0].MAPE) {
		t.Errorf("MAPE = %v, want NaN", got[0].MAPE)
	}
}

func index(s []ZoneStats) map[string]ZoneStats {
	m := map[string]ZoneStats{}
	for _, z := range s {
		m[z.Zone] = z
	}
	return m
}

func TestPredNodeRejectsMixedNodes(t *testing.T) {
	if _, err := PredNode([]Pred{
		{TS: 0, Node: "sut1", Zone: "package", W: 1},
		{TS: 0, Node: "sut2", Zone: "package", W: 2},
	}); err == nil {
		t.Fatal("want an error: the model is per node, so one run is one node")
	}
	got, err := PredNode([]Pred{{TS: 0, Node: "sut1", Zone: "package", W: 1}})
	if err != nil || got != "sut1" {
		t.Errorf("PredNode = %q, %v; want sut1, nil", got, err)
	}
	if _, err := PredNode(nil); err == nil {
		t.Error("want an error for empty predictions")
	}
}

func TestFilterTruthKeepsOneNode(t *testing.T) {
	truth := []Truth{
		{TS: 0, Node: "sut1", Zone: "package", W: 100},
		{TS: 0, Node: "sut2", Zone: "package", W: 7},
		{TS: 10, Node: "sut1", Zone: "package", W: 100},
		{TS: 10, Node: "sut2", Zone: "package", W: 7},
	}
	pred := []Pred{
		{TS: 0, Node: "sut1", Zone: "package", W: 90},
		{TS: 10, Node: "sut1", Zone: "package", W: 110},
	}

	// Unfiltered, the (ts, zone) join takes whichever node's row landed last
	// in the map: sut1's estimate scored against sut2's meter, with an n that
	// looks entirely normal. That is the failure this filter exists to stop.
	wrong := Score(pred, truth)
	if len(wrong) != 1 || wrong[0].MAE < 50 {
		t.Fatalf("expected the unfiltered join to be badly wrong, got %+v", wrong)
	}

	got := Score(pred, FilterTruth(truth, "sut1"))
	if len(got) != 1 {
		t.Fatalf("Score = %+v, want one zone", got)
	}
	if got[0].N != 2 {
		t.Errorf("n = %d, want 2", got[0].N)
	}
	if got[0].MAE != 10 {
		t.Errorf("MAE = %v, want 10 (sut1 truth only)", got[0].MAE)
	}
}

func TestTruthNodesLists(t *testing.T) {
	got := TruthNodes([]Truth{
		{Node: "sut2"}, {Node: "sut1"}, {Node: "sut2"},
	})
	if len(got) != 2 || got[0] != "sut1" || got[1] != "sut2" {
		t.Errorf("TruthNodes = %v, want [sut1 sut2]", got)
	}
}

func TestScoreUnpredictedZoneFails(t *testing.T) {
	pred := []Pred{{TS: 0, Zone: "package", W: 90}}
	truth := []Truth{
		{TS: 0, Zone: "package", W: 100},
		{TS: 0, Zone: "dram", W: 10},
	}
	byZone := index(Score(pred, truth))
	d, ok := byZone["dram"]
	if !ok {
		t.Fatal("an unpredicted truth zone must be listed, not dropped")
	}
	if d.Coverage != 0 || d.N != 0 {
		t.Errorf("dram coverage=%v n=%d, want 0 and 0", d.Coverage, d.N)
	}
	if !math.IsInf(d.MAPE, 1) {
		t.Errorf("dram MAPE = %v, want +Inf: the worst possible zone", d.MAPE)
	}
	if d.EnergyErr != -100 {
		t.Errorf("dram energy err = %v, want -100", d.EnergyErr)
	}
}

func TestScoreGapsAreMisses(t *testing.T) {
	truth := []Truth{
		{TS: 0, Zone: "package", W: 100}, {TS: 10, Zone: "package", W: 100},
		{TS: 20, Zone: "package", W: 100}, {TS: 30, Zone: "package", W: 100},
	}
	pred := []Pred{
		{TS: 0, Zone: "package", W: 100}, {TS: 10, Zone: "package", W: 100},
		{TS: 20, Zone: "package", W: math.NaN()}, // ts 30 absent
	}
	s := index(Score(pred, truth))["package"]
	if s.N != 2 || s.Coverage != 50 {
		t.Errorf("n=%d coverage=%v, want 2 and 50", s.N, s.Coverage)
	}
	if s.MAE != 0 {
		t.Errorf("MAE = %v, want 0 over matched points", s.MAE)
	}
	// Misses count as 0 W: 2000 J predicted against 4000 J measured.
	if math.Abs(s.EnergyErr-(-50)) > 1e-9 {
		t.Errorf("energy err = %v, want -50", s.EnergyErr)
	}
}

func TestCheckPods(t *testing.T) {
	truth := []Truth{{TS: 0, Zone: "package", W: 100}, {TS: 10, Zone: "package", W: 100}}
	pred := []Pred{{TS: 0, Zone: "package", Pods: 100.5}, {TS: 10, Zone: "package", Pods: 100.5}}

	got := CheckPods(pred, truth)
	if len(got) != 1 || got[0].NodeJ != 2000 || got[0].PodJ != 2010 || got[0].NA {
		t.Errorf("CheckPods = %+v, want NodeJ 2000, PodJ 2010", got)
	}
}

func TestLoadTruthSources(t *testing.T) {
	p := filepath.Join(t.TempDir(), "truth.csv")
	body := "ts,metric,node,zone,pod_id,pod_name,pod_namespace,value\n" +
		"10,kepler_node_cpu_watts,sut1,package,,,,7.5\n" +
		"10,kepler_pod_cpu_watts,sut1,package,u,p,ns,1\n" +
		"10,scaph_host_power_microwatts,sut1,,,,,9.334125e+06\n"
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	k, err := loadTruth(p, sources["kepler"])
	if err != nil || len(k) != 1 || k[0].Zone != "package" || k[0].W != 7.5 {
		t.Errorf("kepler truth = %+v, %v", k, err)
	}
	s, err := loadTruth(p, sources["scaphandre"])
	if err != nil || len(s) != 1 || s[0].Zone != "host" || math.Abs(s[0].W-9.334125) > 1e-9 {
		t.Errorf("scaphandre truth = %+v, %v; want zone host, 9.334125 W", s, err)
	}
}

func TestLoadPredToleratesNaNAndEmptyPods(t *testing.T) {
	p := filepath.Join(t.TempDir(), "predictions.csv")
	body := "ts,node,zone,pred_node_w,pred_active_w,pred_pod_w\n" +
		"10,sut1,package,7,3,\"{\"\"a\"\":1,\"\"b\"\":2}\"\n" +
		"20,sut1,package,NaN,NaN,\n"
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := loadPred(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Pods != 3 || got[1].Pods != 0 || !math.IsNaN(got[1].W) {
		t.Errorf("loadPred = %+v", got)
	}
}

func TestSourceNames(t *testing.T) {
	if got := sourceNames(); !strings.Contains(got, "kepler") || !strings.Contains(got, "scaphandre") {
		t.Errorf("sourceNames() = %q", got)
	}
}

// NO OVERLAP is for predictions from the wrong time range. A zone mismatch
// (Kepler zones scored against Scaphandre's "host") must reach the table and
// fail there, with coverage 0 %.
func TestTimesOverlapIgnoresZones(t *testing.T) {
	truth := []Truth{{TS: 10, Zone: "host", W: 9}}
	if !timesOverlap([]Pred{{TS: 10, Zone: "package", W: 1}}, truth) {
		t.Error("same timestamps, different zones: want overlap")
	}
	if timesOverlap([]Pred{{TS: 20, Zone: "host", W: 1}}, truth) {
		t.Error("disjoint timestamps: want no overlap")
	}
}

// An uncore that measures 0 W has no ratio to report, as MAPE already
// treats the same zone as undefined.
func TestCheckPodsNotApplicableAtZeroNodeEnergy(t *testing.T) {
	truth := []Truth{{TS: 0, Zone: "uncore", W: 0}, {TS: 10, Zone: "uncore", W: 0}}
	pred := []Pred{{TS: 0, Zone: "uncore", Pods: 0.2}, {TS: 10, Zone: "uncore", Pods: 0.2}}
	got := CheckPods(pred, truth)
	if len(got) != 1 || !got[0].NA {
		t.Errorf("zero node energy: want n/a, got %+v", got)
	}
}

// pred_pod_w is optional in CONTRACT.md. A file without the column used to
// read column 0 (ts) as JSON and fail.
func TestLoadPredWithoutPodColumn(t *testing.T) {
	p := filepath.Join(t.TempDir(), "predictions.csv")
	body := "ts,node,zone,pred_node_w,pred_active_w,groupname\n" +
		"10,sut1,package,7,3,g\n"
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := loadPred(p)
	if err != nil || len(got) != 1 || got[0].W != 7 || got[0].Pods != 0 {
		t.Errorf("loadPred = %+v, %v; want one row, 7 W, no pods", got, err)
	}
}

// A missing required column must be an error, not column 0 read in its place.
func TestLoadPredMissingRequiredColumn(t *testing.T) {
	p := filepath.Join(t.TempDir(), "predictions.csv")
	if err := os.WriteFile(p, []byte("ts,node,zone\n10,sut1,package\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := loadPred(p); err == nil || !strings.Contains(err.Error(), "pred_node_w") {
		t.Errorf("err = %v, want one naming pred_node_w", err)
	}
}

func TestAlignTruth(t *testing.T) {
	var truth []Truth
	for ts := int64(100); ts <= 160; ts += 10 {
		truth = append(truth, Truth{TS: ts, Zone: "package", W: 1})
	}
	preds := func(start, step int64) []Pred {
		var out []Pred
		for ts := start; ts <= 160; ts += step {
			out = append(out, Pred{TS: ts, Zone: "package", W: 1})
		}
		return out
	}

	// Same step: every truth point stays.
	got, ps, ts, err := AlignTruth(preds(110, 10), truth)
	if err != nil || ps != 10 || ts != 10 || len(got) != 7 {
		t.Errorf("10 s: %d rows, steps %d/%d, %v; want 7 rows, 10/10", len(got), ps, ts, err)
	}
	// A multiple: only truth on the prediction grid is scored, so a 20 s run
	// is not charged for the 10 s points it never claimed.
	got, ps, _, err = AlignTruth(preds(110, 20), truth)
	if err != nil || ps != 20 || len(got) != 3 || got[0].TS != 110 || got[2].TS != 150 {
		t.Errorf("20 s: %+v, step %d, %v; want ts 110,130,150", got, ps, err)
	}
	// A step that is not a multiple of the truth step.
	if _, _, _, err := AlignTruth(preds(110, 15), truth); err == nil {
		t.Error("15 s on a 10 s truth grid: want an error")
	}
	// The right step, shifted off the truth grid.
	if _, _, _, err := AlignTruth(preds(115, 10), truth); err == nil {
		t.Error("10 s grid offset by 5 s: want an error")
	}
	// One stray row off the grid.
	if _, _, _, err := AlignTruth(append(preds(110, 10), Pred{TS: 133}), truth); err == nil {
		t.Error("one off-grid row: want an error")
	}
}

// One NaN truth sample must not turn a zone's MAE, MAPE and energy into NaN.
func TestLoadTruthDropsNonFinite(t *testing.T) {
	p := filepath.Join(t.TempDir(), "truth.csv")
	body := "ts,metric,node,zone,pod_id,pod_name,pod_namespace,value\n" +
		"10,kepler_node_cpu_watts,sut1,package,,,,7.5\n" +
		"20,kepler_node_cpu_watts,sut1,package,,,,NaN\n" +
		"30,kepler_node_cpu_watts,sut1,package,,,,+Inf\n"
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := loadTruth(p, sources["kepler"])
	if err != nil || len(got) != 1 || got[0].TS != 10 {
		t.Errorf("loadTruth = %+v, %v; want only the finite row", got, err)
	}
}

// Truth from strip runs wider than span.txt, the window make score replays,
// and the replay drops its first step. Unclipped, a perfect model tops out at
// about 98 % coverage.
func TestClipTruthToReplayedSpan(t *testing.T) {
	truth := []Truth{{TS: 50}, {TS: 60}, {TS: 70}, {TS: 80}, {TS: 90}}
	got := ClipTruth(truth, 60, 80)
	if len(got) != 2 || got[0].TS != 70 || got[1].TS != 80 {
		t.Errorf("ClipTruth = %+v, want ts 70 and 80 (start excluded: its step is dropped)", got)
	}
}
