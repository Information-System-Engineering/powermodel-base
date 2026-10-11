// Command strip turns a raw Prometheus snapshot into one a model may see:
// truth extracted to CSV, every energy series physically deleted.
//
// The deletion is the whole point. A denylist in a client would be a policy a
// student could route around by accident; deleting the series from the TSDB
// means the data is not there to read.
package main

import (
	"encoding/csv"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"time"

	"github.com/Information-System-Engineering/powermodel-base/signals"
)

// energyNames are the metric names deleted from the TSDB, after truth.csv is
// exported. Broad on purpose: a name that only might carry power is cheaper
// to lose than a measured series left beside the student's signals.
//
// node_hwmon_{power,curr,in}: node-exporter's hwmon collector exposes an ACPI
// power meter (sut-02 has one) and PSU current and voltage sensors whenever
// the kernel driver reports them. ipmi_ and redfish_ exporters carry DCMI
// and PSU power.
const energyNames = `kepler_.*|scaph_.*|.*rapl.*|powermetrics_.*|node_power.*|` +
	`node_hwmon_(power|curr|in)_.*|ipmi_.*|redfish_.*|.*watt.*|.*joule.*`

const energyMatch = `{__name__=~"` + energyNames + `"}`

// truthSeries are exported for scoring. Everything matching energyMatch is
// deleted regardless; this is only what the scorer needs back.
var truthSeries = []string{
	"kepler_node_cpu_watts",
	"kepler_pod_cpu_watts",
	"scaph_host_power_microwatts",
}

func main() {
	var (
		promBin = flag.String("prometheus", "prometheus", "path to the pinned Prometheus binary")
		step    = flag.Duration("step", 10*time.Second,
			"truth export step. score accepts only prediction steps that are multiples of it, "+
				"so pick what a deployment would scrape at: a multiple of the meter's scrape interval")
		port = flag.Int("port", 19099, "port for the temporary Prometheus")
	)
	flag.Parse()
	if flag.NArg() != 2 {
		log.Fatal("usage: strip [flags] <snapshot-dir-or-tar.gz> <out-dir>")
	}
	if err := run(flag.Arg(0), flag.Arg(1), *promBin, *port, *step); err != nil {
		log.Fatalf("strip: %v", err)
	}
}

func run(in, out, promBin string, port int, step time.Duration) error {
	work, err := os.MkdirTemp("", "kepler-powermodel-strip-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(work)

	src := in
	if filepath.Ext(in) == ".gz" {
		src = filepath.Join(work, "extracted")
		if err := os.MkdirAll(src, 0o755); err != nil {
			return err
		}
		log.Printf("extracting %s", in)
		if err := exec.Command("tar", "xzf", in, "-C", src).Run(); err != nil {
			return fmt.Errorf("extract: %w", err)
		}
	}

	// A tarball may hold several top-level directories, only some with blocks.
	// Merging every block into one path is what makes the database whole.
	blocks, err := findBlocks(src)
	if err != nil {
		return err
	}
	log.Printf("found %d TSDB block(s)", len(blocks))

	tsdb := filepath.Join(work, "tsdb")
	if err := os.MkdirAll(tsdb, 0o755); err != nil {
		return err
	}
	for _, b := range blocks {
		dst := filepath.Join(tsdb, filepath.Base(b))
		if err := exec.Command("cp", "-R", b, dst).Run(); err != nil {
			return fmt.Errorf("copy block %s: %w", b, err)
		}
	}

	minT, maxT, err := blockSpan(blocks)
	if err != nil {
		return err
	}
	log.Printf("span %s .. %s", minT.UTC().Format(time.RFC3339), maxT.UTC().Format(time.RFC3339))

	stop, base, err := serve(promBin, tsdb, work, port)
	if err != nil {
		return err
	}
	defer stop()

	client := signals.New(base)
	if err := exportTruth(client, filepath.Join(out, "truth.csv"), minT, maxT, step); err != nil {
		// A refused or failed export must not leave a header-only truth.csv
		// behind. On disk it is indistinguishable from a real one until the
		// scorer reports zero overlap, hours later and somewhere else.
		os.Remove(filepath.Join(out, "truth.csv"))
		return err
	}
	if err := deleteEnergy(base); err != nil {
		return err
	}
	snapDir, err := takeSnapshot(base, tsdb)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(out, 0o755); err != nil {
		return err
	}
	dst := filepath.Join(out, "tsdb")
	_ = os.RemoveAll(dst)
	if err := exec.Command("cp", "-R", snapDir, dst).Run(); err != nil {
		return fmt.Errorf("copy snapshot: %w", err)
	}
	stop()

	// Record a QUERYABLE span so a student does not have to guess a window.
	//
	// Not the raw block bounds: at exactly minTime no sample has landed yet,
	// and the informer's rate([1m]) needs a minute of history behind it, so a
	// replay starting at minTime reads no CPU on its first steps. Trimming
	// here rather than in the caller keeps the knowledge next to the metadata.
	const rateWindow = time.Minute
	spanPath := filepath.Join(out, "span.txt")
	spanTxt := fmt.Sprintf("%s..%s\n",
		minT.Add(rateWindow).UTC().Format(time.RFC3339),
		maxT.Add(-step).UTC().Format(time.RFC3339))
	if err := os.WriteFile(spanPath, []byte(spanTxt), 0o644); err != nil {
		return err
	}
	log.Printf("wrote %s: %s", spanPath, spanTxt)

	return verify(promBin, dst, work, port+1, minT, maxT)
}

// blockSpan reads meta.json from every block and returns the union of spans.
func blockSpan(blocks []string) (time.Time, time.Time, error) {
	var minMs, maxMs int64
	for i, b := range blocks {
		raw, err := os.ReadFile(filepath.Join(b, "meta.json"))
		if err != nil {
			return time.Time{}, time.Time{}, err
		}
		var meta struct {
			MinTime int64 `json:"minTime"`
			MaxTime int64 `json:"maxTime"`
		}
		if err := json.Unmarshal(raw, &meta); err != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("%s/meta.json: %w", b, err)
		}
		if i == 0 || meta.MinTime < minMs {
			minMs = meta.MinTime
		}
		if i == 0 || meta.MaxTime > maxMs {
			maxMs = meta.MaxTime
		}
	}
	return time.UnixMilli(minMs), time.UnixMilli(maxMs), nil
}

// serve starts Prometheus on dir and waits for it to answer.
func serve(bin, dir, work string, port int) (func(), string, error) {
	// Prometheus will not start without a config file, even for a static TSDB.
	cfg := filepath.Join(work, fmt.Sprintf("prom-%d.yml", port))
	if err := os.WriteFile(cfg, []byte("global: {}\n"), 0o644); err != nil {
		return nil, "", err
	}
	base := fmt.Sprintf("http://127.0.0.1:%d", port)
	cmd := exec.Command(bin,
		"--config.file="+cfg,
		"--storage.tsdb.path="+dir,
		"--storage.tsdb.retention.time=10y",
		"--web.listen-address="+fmt.Sprintf("127.0.0.1:%d", port),
		"--web.enable-admin-api",
	)
	cmd.Stdout, cmd.Stderr = os.Stderr, os.Stderr
	if err := cmd.Start(); err != nil {
		return nil, "", fmt.Errorf("start prometheus: %w", err)
	}
	stop := func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
			_, _ = cmd.Process.Wait()
			cmd.Process = nil
		}
	}
	for range 60 {
		resp, err := http.Get(base + "/-/ready")
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == 200 {
				return stop, base, nil
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	stop()
	return nil, "", fmt.Errorf("prometheus did not become ready on %s", base)
}

func exportTruth(c *signals.Client, path string, start, end time.Time, step time.Duration) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	w := csv.NewWriter(f)
	defer w.Flush()
	if err := w.Write([]string{"ts", "metric", "node", "zone", "pod_id", "pod_name", "pod_namespace", "value"}); err != nil {
		return err
	}

	rows := 0
	for _, metric := range truthSeries {
		m, err := c.QueryRange(metric, start, end, step)
		if err != nil {
			return fmt.Errorf("export %s: %w", metric, err)
		}
		ts := make([]int64, 0, len(m))
		for t := range m {
			ts = append(ts, t)
		}
		slices.Sort(ts)
		for _, t := range ts {
			if err := checkNotSynthetic(metric, m[t]); err != nil {
				return err
			}
			for _, s := range m[t] {
				node := s.Labels["node_name"]
				if node == "" {
					node = s.Labels["node"]
				}
				if err := w.Write([]string{
					strconv.FormatInt(t, 10), metric, node, s.Labels["zone"],
					s.Labels["pod_id"], s.Labels["pod_name"], s.Labels["pod_namespace"],
					strconv.FormatFloat(s.Value, 'g', -1, 64),
				}); err != nil {
					return err
				}
				rows++
			}
		}
		log.Printf("exported %s", metric)
	}
	// Nothing to score against means the snapshot is useless for this project,
	// and finding that out now beats finding it out after a replay.
	if rows == 0 {
		return fmt.Errorf("no truth rows exported: snapshot carries none of %v", truthSeries)
	}
	log.Printf("wrote %s (%d rows)", path, rows)
	return nil
}

// checkNotSynthetic refuses to build truth out of fabricated power.
//
// power-kind's exporter emits P = 50 + 100*u split package 0.85 / dram 0.15,
// a fixed function of CPU. A snapshot taken from that cluster would let a
// model score perfectly by learning that function and nothing else, so the
// refusal belongs here, at the source, rather than in a warning nobody reads.
// Every fabricated series carries synthetic="true".
func checkNotSynthetic(metric string, v signals.Vector) error {
	for _, s := range v {
		if s.Labels["synthetic"] == "true" {
			return fmt.Errorf(
				"refusing to export %s: series carries synthetic=\"true\" (labels %v). "+
					"This snapshot's power is fabricated — scoring against it is meaningless. "+
					"Take a snapshot from a node with a real power source",
				metric, s.Labels)
		}
	}
	return nil
}

func post(base, path string, q url.Values) error {
	resp, err := http.PostForm(base+path, q)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("%s: HTTP %d", path, resp.StatusCode)
	}
	return nil
}

func deleteEnergy(base string) error {
	if err := post(base, "/api/v1/admin/tsdb/delete_series", url.Values{"match[]": {energyMatch}}); err != nil {
		return err
	}
	log.Print("deleted energy series")
	return post(base, "/api/v1/admin/tsdb/clean_tombstones", nil)
}

func takeSnapshot(base, tsdb string) (string, error) {
	resp, err := http.Post(base+"/api/v1/admin/tsdb/snapshot", "", nil)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var body struct {
		Status string `json:"status"`
		Data   struct {
			Name string `json:"name"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "", err
	}
	if body.Status != "success" || body.Data.Name == "" {
		return "", fmt.Errorf("snapshot failed: %+v", body)
	}
	return filepath.Join(tsdb, "snapshots", body.Data.Name), nil
}

// verify restarts Prometheus on the stripped output and asserts the energy
// series are gone and ordinary signals remain. Without both halves a strip
// that deleted everything would pass.
func verify(bin, dir, work string, port int, start, end time.Time) error {
	stop, base, err := serve(bin, dir, work, port)
	if err != nil {
		return err
	}
	defer stop()

	c := signals.New(base)
	at := start.Add(end.Sub(start) / 2)

	left, err := c.Query(`count(`+energyMatch+`)`, at)
	if err != nil {
		return err
	}
	if len(left) > 0 && left[0].Value > 0 {
		return fmt.Errorf("STRIP FAILED: %v energy series still present at %s",
			left[0].Value, at.UTC().Format(time.RFC3339))
	}

	cpu, err := c.Query(`count(node_cpu_seconds_total)`, at)
	if err != nil {
		return err
	}
	if len(cpu) == 0 || cpu[0].Value == 0 {
		return fmt.Errorf("STRIP FAILED: no node_cpu_seconds_total left either — the snapshot is empty, not stripped")
	}
	log.Printf("verified: 0 energy series, %v node CPU series remain", cpu[0].Value)
	return nil
}
