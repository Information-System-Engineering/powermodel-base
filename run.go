// Package keplerpowermodel holds the run loops. The packages below it hold the
// Kepler-shaped pieces those loops drive.
//
//	Replay -- over a snapshot, producing predictions to score
//	Serve  -- in a live cluster, exposing powermodel_* on /metrics
package keplerpowermodel

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/Information-System-Engineering/powermodel-base/device"
	"github.com/Information-System-Engineering/powermodel-base/monitor"
	"github.com/Information-System-Engineering/powermodel-base/resource"
)

// Replay steps the clock across the window and writes one prediction row per
// zone per step.
//
// The first step is refreshed but not written, as CONTRACT.md asks; score
// clips truth to (start, end] to match.
//
// group becomes the groupname column from CONTRACT.md.
func Replay(mon *monitor.Monitor, refresh func() error, clk *resource.StepClock,
	end time.Time, group, outPath string) error {

	if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
		return err
	}
	f, err := os.Create(outPath)
	if err != nil {
		return err
	}
	defer f.Close()
	w := csv.NewWriter(f)

	if err := w.Write([]string{"ts", "node", "zone", "pred_node_w", "pred_active_w", "groupname", "pred_pod_w"}); err != nil {
		return err
	}

	written, first := 0, true
	for t := clk.Now(); !t.After(end); t = clk.Advance() {
		snap, err := step(mon, refresh, t)
		if err != nil {
			return err
		}
		if first {
			first = false
			continue
		}
		if err := writeSnapshot(w, t, mon, snap, group); err != nil {
			return err
		}
		written++
	}
	if written == 0 {
		return fmt.Errorf("zero steps written: is the range inside the snapshot's span?")
	}
	w.Flush()
	if err := w.Error(); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	log.Printf("wrote %s (%d steps)", outPath, written)
	return nil
}

// step refreshes and collects once, naming the timestamp on any failure. A
// replay that dies on step 900 of 1400 should say which step.
func step(mon *monitor.Monitor, refresh func() error, t time.Time) (*monitor.Snapshot, error) {
	if err := refresh(); err != nil {
		return nil, fmt.Errorf("at %s: %w", t.UTC().Format(time.RFC3339), err)
	}
	snap, err := mon.Collect()
	if err != nil {
		return nil, fmt.Errorf("at %s: %w", t.UTC().Format(time.RFC3339), err)
	}
	return snap, nil
}

// writeSnapshot writes one predictions.csv row per zone.
func writeSnapshot(w *csv.Writer, t time.Time, mon *monitor.Monitor, snap *monitor.Snapshot, group string) error {
	zones := make([]string, 0, len(snap.Node))
	for z := range snap.Node {
		zones = append(zones, string(z))
	}
	slices.Sort(zones)

	for _, z := range zones {
		zone := device.Zone(z)
		pods := map[string]float64{}
		for id, p := range snap.Pods {
			pods[id] = float64(p.Zones[zone])
		}
		blob, _ := json.Marshal(pods)
		row := []string{
			strconv.FormatInt(t.Unix(), 10), mon.NodeName(), z,
			strconv.FormatFloat(float64(snap.Node[zone]), 'g', -1, 64),
			strconv.FormatFloat(float64(snap.Active[zone]), 'g', -1, 64),
			group, string(blob),
		}
		if err := w.Write(row); err != nil {
			return err
		}
	}
	return nil
}

// Serve exposes /metrics for a live cluster scrape. The monitor is built once
// by the caller and lives as long as the process.
//
// group becomes the groupname label from CONTRACT.md. It is required: on a
// shared cluster, series without it cannot be told apart by group.
func Serve(mon *monitor.Monitor, refresh func() error, node, group, addr string) error {
	if group == "" {
		return fmt.Errorf("group is required in online mode: it becomes the groupname label")
	}
	http.HandleFunc("/metrics", metricsHandler(mon, refresh, node, group))
	log.Printf("serving /metrics on %s as groupname=%q", addr, group)
	return http.ListenAndServe(addr, nil)
}

func metricsHandler(mon *monitor.Monitor, refresh func() error, node, group string) http.HandlerFunc {
	// One informer, one meter: two scrapes at once must not refresh them
	// concurrently.
	var mu sync.Mutex
	return func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		// A model that fails must fail the scrape. Emitting zero watts would
		// be a number pretending to be an estimate.
		if err := refresh(); err != nil {
			http.Error(w, err.Error(), http.StatusServiceUnavailable)
			return
		}
		snap, err := mon.Collect()
		if err != nil {
			http.Error(w, err.Error(), http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		fmt.Fprintln(w, "# HELP powermodel_node_watts Estimated node power per zone (CONTRACT.md)")
		fmt.Fprintln(w, "# TYPE powermodel_node_watts gauge")
		for zone, watts := range snap.Node {
			fmt.Fprintf(w, "powermodel_node_watts{node_name=%q,zone=%q,groupname=%q} %.4f\n", node, zone, group, watts)
		}
		fmt.Fprintln(w, "# HELP powermodel_pod_watts Estimated pod power per zone (CONTRACT.md)")
		fmt.Fprintln(w, "# TYPE powermodel_pod_watts gauge")
		for _, p := range snap.Pods {
			for zone, watts := range p.Zones {
				fmt.Fprintf(w, "powermodel_pod_watts{node_name=%q,zone=%q,groupname=%q,pod_namespace=%q,pod_name=%q} %.4f\n",
					node, zone, group, p.Namespace, p.Name, watts)
			}
		}
	}
}
