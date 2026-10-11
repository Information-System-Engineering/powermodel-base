package keplerpowermodel

import (
	"encoding/csv"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Information-System-Engineering/powermodel-base/device"
	"github.com/Information-System-Engineering/powermodel-base/monitor"
	"github.com/Information-System-Engineering/powermodel-base/resource"
	"github.com/Information-System-Engineering/powermodel-base/signals"
)

// stubInformer stands in for PromInformer so the run loops can be exercised
// without a Prometheus.
type stubInformer struct {
	node *resource.Node
	pods *resource.Pods
}

func (s *stubInformer) Init() error          { return nil }
func (s *stubInformer) Refresh() error       { return nil }
func (s *stubInformer) Node() *resource.Node { return s.node }
func (s *stubInformer) Pods() *resource.Pods { return s.pods }

// mapQuerier answers by exact expression, at any time.
type mapQuerier map[string]signals.Vector

func (q mapQuerier) Query(expr string, _ time.Time) (signals.Vector, error) {
	return q[expr], nil
}

// testMonitor returns a monitor whose meter reports 100 W package, with the
// stub informer's running pods for a test to fill.
func testMonitor(t *testing.T) (*monitor.Monitor, func() error, *resource.StepClock, map[string]*resource.Pod) {
	t.Helper()
	clk := resource.NewStepClock(time.Unix(1000, 0), 10*time.Second)
	node := resource.NewNode(mapQuerier{}, clk.Now(), 10*time.Second)
	node.CPUUsageRatio = 0.5
	node.ProcessTotalCPUTimeDelta = 5
	inf := &stubInformer{
		node: node,
		pods: &resource.Pods{
			Running:    map[string]*resource.Pod{},
			Terminated: map[string]*resource.Pod{},
		},
	}
	m := device.NewModelMeter(func() (map[device.Zone]device.Power, error) {
		return map[device.Zone]device.Power{"package": 100}, nil
	}, 10*time.Second)
	refresh := func() error {
		if err := inf.Refresh(); err != nil {
			return err
		}
		return m.Refresh()
	}
	return monitor.New(m, inf, "sut1"), refresh, clk, inf.pods.Running
}

func readCSV(t *testing.T, path string) [][]string {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	rows, err := csv.NewReader(f).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	return rows
}

func TestReplayWritesHeaderAndDropsFirstStep(t *testing.T) {
	mon, refresh, clk, _ := testMonitor(t)
	out := filepath.Join(t.TempDir(), "predictions.csv")

	end := clk.Now().Add(30 * time.Second)
	if err := Replay(mon, refresh, clk, end, "g1", out); err != nil {
		t.Fatal(err)
	}

	rows := readCSV(t, out)
	want := []string{"ts", "node", "zone", "pred_node_w", "pred_active_w", "groupname", "pred_pod_w"}
	if len(rows) == 0 {
		t.Fatal("no rows written")
	}
	for i, h := range want {
		if rows[0][i] != h {
			t.Errorf("header[%d] = %q, want %q", i, rows[0][i], h)
		}
	}
	// Steps at t0, t0+10, t0+20, t0+30 is four; the first is dropped, as
	// CONTRACT.md asks. One zone in the fixture.
	if got := len(rows) - 1; got != 3 {
		t.Errorf("data rows = %d, want 3 (first step dropped)", got)
	}
	if rows[1][1] != "sut1" {
		t.Errorf("node column = %q, want sut1", rows[1][1])
	}
	if rows[1][5] != "g1" {
		t.Errorf("groupname column = %q, want g1", rows[1][5])
	}
}

func TestReplayFailsOnZeroSteps(t *testing.T) {
	mon, refresh, clk, _ := testMonitor(t)
	out := filepath.Join(t.TempDir(), "predictions.csv")
	// An end before the start means the range is outside the snapshot's span,
	// which is the most common way a replay produces nothing at all.
	if err := Replay(mon, refresh, clk, clk.Now().Add(-time.Hour), "g1", out); err == nil {
		t.Fatal("want an error for a range that yields no steps")
	}
}

func TestServeExposesContractMetrics(t *testing.T) {
	mon, refresh, _, pods := testMonitor(t)
	// One running pod with half the node's CPU work: 2.5 of 5 seconds.
	pods["demo/a"] = &resource.Pod{
		ID: "demo/a", Name: "a", Namespace: "demo", CPUTimeDelta: 2.5,
	}
	h := metricsHandler(mon, refresh, "sut1", "g1")

	rec := httptest.NewRecorder()
	h(rec, httptest.NewRequest("GET", "/metrics", nil))
	if rec.Code != 200 {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()

	// node: 100 W package from testMonitor's meter.
	// pod: active = 100 x 0.5 = 50 W; share 2.5/5 = 0.5 -> 25 W.
	for _, want := range []string{
		`powermodel_node_watts{node_name="sut1",zone="package",groupname="g1"} 100.0000`,
		`powermodel_pod_watts{node_name="sut1",zone="package",groupname="g1",pod_namespace="demo",pod_name="a"} 25.0000`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing line %q in:\n%s", want, body)
		}
	}
	for _, banned := range []string{"kepler_", "synthetic"} {
		if strings.Contains(body, banned) {
			t.Errorf("body still mentions %q:\n%s", banned, body)
		}
	}
}

func TestServeRequiresGroup(t *testing.T) {
	mon, refresh, _, _ := testMonitor(t)
	err := Serve(mon, refresh, "sut1", "", "127.0.0.1:0")
	if err == nil || !strings.Contains(err.Error(), "groupname") {
		t.Fatalf("err = %v, want one naming groupname", err)
	}
}

func TestServeFailsLoudWhenTheModelFails(t *testing.T) {
	mon, _, _, _ := testMonitor(t)
	h := metricsHandler(mon, func() error { return errors.New("model exploded") }, "sut1", "g1")
	rec := httptest.NewRecorder()
	h(rec, httptest.NewRequest("GET", "/metrics", nil))
	// Zero watts would be a number pretending to be an estimate.
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", rec.Code)
	}
}

// The handler used to build a fresh monitor per scrape. It now reuses one,
// and two scrapes arriving together must not refresh it concurrently.
func TestServeSerializesScrapes(t *testing.T) {
	mon, refresh, _, _ := testMonitor(t)
	var inside, overlap, calls atomic.Int32
	h := metricsHandler(mon, func() error {
		calls.Add(1)
		if inside.Add(1) > 1 {
			overlap.Store(1)
		}
		time.Sleep(10 * time.Millisecond)
		inside.Add(-1)
		return refresh()
	}, "sut1", "g1")

	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rec := httptest.NewRecorder()
			h(rec, httptest.NewRequest("GET", "/metrics", nil))
			if rec.Code != 200 {
				t.Errorf("status = %d", rec.Code)
			}
		}()
	}
	wg.Wait()
	if calls.Load() != 4 {
		t.Errorf("refresh calls = %d, want 4 (one per scrape)", calls.Load())
	}
	if overlap.Load() != 0 {
		t.Error("two scrapes refreshed the shared informer at once")
	}
}
