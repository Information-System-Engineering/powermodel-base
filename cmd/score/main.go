// Command score grades a predictions.csv against a snapshot's truth.csv.
//
//	score out/predictions.csv --truth <stripped>/truth.csv [--source kepler|scaphandre]
//
// It reports scores only, no pass or fail: exit 0 whenever it could score.
package main

import (
	"encoding/csv"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	lib "github.com/Information-System-Engineering/powermodel-base"
)

// Source is one truth metric the scorer can grade against.
type Source struct {
	Metric string
	Scale  float64 // to watts
	Zone   string  // fixed zone for zoneless metrics; empty means use the row's
}

var sources = map[string]Source{
	"kepler":     {Metric: "kepler_node_cpu_watts", Scale: 1},
	"scaphandre": {Metric: "scaph_host_power_microwatts", Scale: 1e-6, Zone: "host"},
}

func sourceNames() string {
	names := make([]string, 0, len(sources))
	for n := range sources {
		names = append(names, n)
	}
	slices.Sort(names)
	return strings.Join(names, "|")
}

func main() {
	log.SetFlags(0)
	fs := flag.NewFlagSet("score", flag.ExitOnError)
	truthPath := fs.String("truth", "", "path to the snapshot's truth.csv")
	source := fs.String("source", "kepler", "truth source: kepler | scaphandre")
	// The predictions path comes first and the flag after it, the way the
	// Python took them; Go's flag package stops at the first positional, so
	// collect them one at a time and keep parsing what follows.
	var pos []string
	must(fs.Parse(os.Args[1:]))
	for fs.NArg() > 0 {
		pos = append(pos, fs.Arg(0))
		must(fs.Parse(fs.Args()[1:]))
	}
	if len(pos) != 1 || *truthPath == "" {
		fmt.Fprintln(os.Stderr, "usage: score <predictions.csv> --truth <truth.csv> [--source kepler|scaphandre]")
		os.Exit(2)
	}
	os.Exit(run(pos[0], *truthPath, *source))
}

func must(err error) {
	if err != nil {
		log.Fatal(err)
	}
}

func run(predPath, truthPath, source string) int {
	src, ok := sources[source]
	if !ok {
		fmt.Fprintf(os.Stderr, "unknown --source %q: want %s\n", source, sourceNames())
		return 2
	}
	pred, err := loadPred(predPath)
	if err != nil {
		log.Fatalf("predictions: %v", err)
	}
	truth, err := loadTruth(truthPath, src)
	if err != nil {
		log.Fatalf("truth: %v", err)
	}
	// The model is per node, so scoring is per node. Take the node from the
	// predictions rather than a flag: a replay already ran with one -node, and
	// a flag here would let someone score one node's estimates against
	// another node's meter.
	node, err := PredNode(pred)
	if err != nil {
		log.Fatalf("predictions: %v", err)
	}
	all := truth
	truth = FilterTruth(truth, node)
	if len(truth) == 0 {
		log.Fatalf("truth carries no %s rows for node %q (it has %v). "+
			"Either this snapshot has no %s, or -node did not match "+
			"the node label cmd/strip extracted", src.Metric, node, TruthNodes(all), source)
	}

	// span.txt is the window make score replays, written by cmd/strip next to
	// truth.csv. Truth outside it is a miss no model could avoid.
	if b, err := os.ReadFile(filepath.Join(filepath.Dir(truthPath), "span.txt")); err == nil {
		start, end, err := lib.ParseRange(strings.TrimSpace(string(b)))
		if err != nil {
			log.Fatalf("span.txt: %v", err)
		}
		truth = ClipTruth(truth, start.Unix(), end.Unix())
		if len(truth) == 0 {
			log.Fatalf("no %s rows inside span.txt (%s)", src.Metric, strings.TrimSpace(string(b)))
		}
	}

	truth, predStep, truthStep, err := AlignTruth(pred, truth)
	if err != nil {
		log.Fatalf("predictions: %v", err)
	}

	table := Score(pred, truth)
	if !timesOverlap(pred, truth) {
		lo, hi := tsRange(pred, func(p Pred) int64 { return p.TS })
		tlo, thi := tsRange(truth, func(t Truth) int64 { return t.TS })
		fmt.Fprintln(os.Stderr, "NO OVERLAP between predictions and truth.")
		fmt.Fprintf(os.Stderr, "  prediction ts %d..%d zones %v\n", lo, hi, zonesOf(pred))
		fmt.Fprintf(os.Stderr, "  truth      ts %d..%d zones %v\n", tlo, thi, zonesOfTruth(truth))
		return 2
	}

	fmt.Printf("\ntruth: %s (%s)\n", source, src.Metric)
	fmt.Printf("step: predictions %d s, truth %d s\n", predStep, truthStep)
	fmt.Printf("predicted zones: %v\n", zonesOf(pred))
	fmt.Printf("truth zones:     %v\n", zonesOfTruth(truth))
	crossCheck(truthPath, source, node)
	fmt.Println()
	fmt.Printf("%-10s %5s %10s %10s %10s %10s %14s\n", "zone", "n", "coverage_%", "MAE_W", "RMSE_W", "MAPE_%", "energy_err_%")
	worst := math.NaN()
	for _, z := range table {
		fmt.Printf("%-10s %5d %10.1f %10.3f %10.3f %10.3f %14.3f\n", z.Zone, z.N, z.Coverage, z.MAE, z.RMSE, z.MAPE, z.EnergyErr)
		if !math.IsNaN(z.MAPE) && (math.IsNaN(worst) || z.MAPE > worst) {
			worst = z.MAPE
		}
	}

	fmt.Printf("\nworst zone MAPE: %.2f%%\n", worst)

	fmt.Printf("\npod energy vs node energy\n")
	fmt.Printf("%-10s %14s %14s %8s\n", "zone", "pods_J", "node_J", "ratio")
	for _, c := range CheckPods(pred, truth) {
		ratio := fmt.Sprintf("%8.3f", c.PodJ/c.NodeJ)
		if c.NA {
			ratio = "     n/a  (node measured 0 J)"
		}
		fmt.Printf("%-10s %14.1f %14.1f %s\n", c.Zone, c.PodJ, c.NodeJ, ratio)
	}
	return 0
}

// timesOverlap reports whether any truth timestamp has a prediction row, in
// any zone. Zones are left out on purpose: a zone mismatch is a scoring
// result (coverage 0 %), not a sign the replay ran over the wrong range.
func timesOverlap(pred []Pred, truth []Truth) bool {
	seen := make(map[int64]bool, len(pred))
	for _, p := range pred {
		seen[p.TS] = true
	}
	for _, t := range truth {
		if seen[t.TS] {
			return true
		}
	}
	return false
}

// crossCheck prints the unselected source's mean watts next to the selected
// one's. It is a sanity check on the meters, not a score.
func crossCheck(truthPath, selected, node string) {
	names := strings.Split(sourceNames(), "|")
	for _, name := range names {
		t, err := loadTruth(truthPath, sources[name])
		if err != nil {
			continue
		}
		t = FilterTruth(t, node)
		if len(t) == 0 {
			continue
		}
		sum, n := map[string]float64{}, map[string]int{}
		for _, r := range t {
			sum[r.Zone] += r.W
			n[r.Zone]++
		}
		tag := "cross-check, not scored"
		if name == selected {
			tag = "scored"
		}
		fmt.Printf("mean W %-11s", name+":")
		for _, z := range zonesOfTruth(t) {
			fmt.Printf(" %s=%.2f", z, sum[z]/float64(n[z]))
		}
		fmt.Printf("  (%s)\n", tag)
	}
}

func loadPred(path string) ([]Pred, error) {
	rows, head, err := readCSV(path, "ts", "node", "zone", "pred_node_w")
	if err != nil {
		return nil, err
	}
	podCol, hasPods := head["pred_pod_w"] // optional in CONTRACT.md
	var out []Pred
	for i, r := range rows {
		ts, err := strconv.ParseInt(r[head["ts"]], 10, 64)
		if err != nil {
			return nil, fmt.Errorf("row %d: ts: %w", i+2, err)
		}
		w, err := strconv.ParseFloat(r[head["pred_node_w"]], 64)
		if err != nil {
			return nil, fmt.Errorf("row %d: pred_node_w: %w", i+2, err)
		}
		pods := 0.0
		if raw := r[podCol]; hasPods && raw != "" {
			var m map[string]float64
			if err := json.Unmarshal([]byte(raw), &m); err != nil {
				return nil, fmt.Errorf("row %d: pred_pod_w: %w", i+2, err)
			}
			for _, v := range m {
				if !math.IsNaN(v) && !math.IsInf(v, 0) {
					pods += v
				}
			}
		}
		out = append(out, Pred{TS: ts, Node: r[head["node"]], Zone: r[head["zone"]], W: w, Pods: pods})
	}
	return out, nil
}

func loadTruth(path string, src Source) ([]Truth, error) {
	rows, head, err := readCSV(path, "ts", "metric", "node", "zone", "value")
	if err != nil {
		return nil, err
	}
	var out []Truth
	for i, r := range rows {
		// Node watts only: pod rows and the other source are a different
		// quantity and would join against nothing but noise.
		if r[head["metric"]] != src.Metric {
			continue
		}
		zone := r[head["zone"]]
		if src.Zone != "" {
			zone = src.Zone
		}
		if zone == "" {
			continue
		}
		ts, err := strconv.ParseInt(r[head["ts"]], 10, 64)
		if err != nil {
			return nil, fmt.Errorf("row %d: ts: %w", i+2, err)
		}
		w, err := strconv.ParseFloat(r[head["value"]], 64)
		if err != nil {
			return nil, fmt.Errorf("row %d: value: %w", i+2, err)
		}
		// A NaN meter sample is no truth at all; kept, it would turn the
		// whole zone's MAE, MAPE and energy into NaN.
		if math.IsNaN(w) || math.IsInf(w, 0) {
			continue
		}
		out = append(out, Truth{TS: ts, Node: r[head["node"]], Zone: zone, W: w * src.Scale})
	}
	return out, nil
}

// readCSV returns the data rows plus a column-name index. Every required
// column must be in the header: a missing key in the index would read as
// column 0, silently the wrong field.
func readCSV(path string, required ...string) ([][]string, map[string]int, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	defer f.Close()
	r := csv.NewReader(f)
	header, err := r.Read()
	if err == io.EOF {
		return nil, nil, fmt.Errorf("%s is empty", path)
	}
	if err != nil {
		return nil, nil, err
	}
	head := make(map[string]int, len(header))
	for i, h := range header {
		head[h] = i
	}
	for _, c := range required {
		if _, ok := head[c]; !ok {
			return nil, nil, fmt.Errorf("%s: missing column %q (header %v)", path, c, header)
		}
	}
	rows, err := r.ReadAll()
	return rows, head, err
}

func zonesOf(pred []Pred) []string {
	seen := map[string]bool{}
	for _, p := range pred {
		seen[p.Zone] = true
	}
	return sortedKeys(seen)
}

func zonesOfTruth(truth []Truth) []string {
	seen := map[string]bool{}
	for _, t := range truth {
		seen[t.Zone] = true
	}
	return sortedKeys(seen)
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

func tsRange[T any](rows []T, ts func(T) int64) (int64, int64) {
	if len(rows) == 0 {
		return 0, 0
	}
	lo, hi := ts(rows[0]), ts(rows[0])
	for _, r := range rows[1:] {
		lo, hi = min(lo, ts(r)), max(hi, ts(r))
	}
	return lo, hi
}
