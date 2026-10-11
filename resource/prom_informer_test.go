package resource

import (
	"strings"
	"testing"
	"time"

	"github.com/Information-System-Engineering/powermodel-base/signals"
)

// fakeQuerier answers by substring match on the expression, so a test states
// what it means rather than reproducing exact PromQL.
type fakeQuerier struct {
	answers map[string]signals.Vector
	asked   []string
}

func (f *fakeQuerier) Query(expr string, _ time.Time) (signals.Vector, error) {
	f.asked = append(f.asked, expr)
	for k, v := range f.answers {
		if strings.Contains(expr, k) {
			return v, nil
		}
	}
	return signals.Vector{}, nil
}

// timedQuerier answers one value per unix second, whatever the expression.
type timedQuerier map[int64]float64

func (q timedQuerier) Query(_ string, t time.Time) (signals.Vector, error) {
	v, ok := q[t.Unix()]
	if !ok {
		return signals.Vector{}, nil
	}
	return vec(v, nil), nil
}

func vec(v float64, labels map[string]string) signals.Vector {
	return signals.Vector{{Labels: labels, Value: v}}
}

func TestPromInformerFillsNodeAndPods(t *testing.T) {
	q := &fakeQuerier{answers: map[string]signals.Vector{
		`mode!="idle"`: vec(4.0, nil), // 4 non-idle CPU-seconds per second
		`mode="idle"`:  vec(8.0, nil), // 8 cores
		"container_cpu_usage": {
			{Labels: map[string]string{"pod": "a", "namespace": "demo", "id": "uid-a"}, Value: 3.0},
			{Labels: map[string]string{"pod": "b", "namespace": "demo", "id": "uid-b"}, Value: 1.0},
		},
	}}

	inf := NewPromInformer(q, NewStepClock(time.Unix(1788950000, 0), 10*time.Second), "sut1", 10*time.Second)
	if err := inf.Refresh(); err != nil {
		t.Fatalf("Refresh: %v", err)
	}

	n := inf.Node()
	if n.CPUUsageRatio != 0.5 {
		t.Fatalf("CPUUsageRatio: got %v, want 0.5 (4 busy of 8 cores)", n.CPUUsageRatio)
	}
	if n.ProcessTotalCPUTimeDelta != 40.0 {
		t.Fatalf("ProcessTotalCPUTimeDelta: got %v, want 40 (4 cores-busy x 10 s step)", n.ProcessTotalCPUTimeDelta)
	}

	p := inf.Pods()
	if len(p.Running) != 2 {
		t.Fatalf("got %d pods, want 2", len(p.Running))
	}
	if got := p.Running["demo/a"].CPUTimeDelta; got != 30.0 {
		t.Fatalf("pod a CPUTimeDelta: got %v, want 30", got)
	}
	if p.Running["demo/a"].Namespace != "demo" || p.Running["demo/a"].Name != "a" {
		t.Fatalf("pod labels lost: %+v", p.Running["demo/a"])
	}
}

// The step used to be a 10 s constant while -step drove cache and clock, so
// -step=5s doubled every CPU delta.
func TestPromInformerUsesCallerStep(t *testing.T) {
	q := &fakeQuerier{answers: map[string]signals.Vector{
		`mode!="idle"`: vec(4.0, nil),
		`mode="idle"`:  vec(8.0, nil),
		"container_cpu_usage": {
			{Labels: map[string]string{"pod": "a", "namespace": "demo", "id": "uid-a"}, Value: 3.0},
		},
	}}
	inf := NewPromInformer(q, NewStepClock(time.Unix(1000, 0), 5*time.Second), "sut1", 5*time.Second)
	if err := inf.Refresh(); err != nil {
		t.Fatal(err)
	}
	if got := inf.Node().ProcessTotalCPUTimeDelta; got != 20 {
		t.Errorf("ProcessTotalCPUTimeDelta = %v, want 20 (4 busy x 5 s)", got)
	}
	if got := inf.Node().StepSeconds; got != 5 {
		t.Errorf("StepSeconds = %v, want 5", got)
	}
	if got := inf.Node().Name; got != "sut1" {
		t.Errorf("Name = %q, want sut1: a model needs it to filter its selectors", got)
	}
	if got := inf.Pods().Running["demo/a"].CPUTimeDelta; got != 15 {
		t.Errorf("pod CPUTimeDelta = %v, want 15 (3 cores x 5 s)", got)
	}
}

func TestPromInformerRatioIsZeroWhenNoCores(t *testing.T) {
	q := &fakeQuerier{answers: map[string]signals.Vector{}}
	inf := NewPromInformer(q, NewStepClock(time.Unix(1, 0), 10*time.Second), "sut1", 10*time.Second)
	if err := inf.Refresh(); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if inf.Node().CPUUsageRatio != 0 {
		t.Fatal("no core count must give a zero ratio, not a divide by zero")
	}
}

func TestGetRejectsAggregatesBeforeQuerying(t *testing.T) {
	q := &fakeQuerier{answers: map[string]signals.Vector{}}
	n := NewNode(q, time.Unix(1000, 0), 10*time.Second)
	for _, sel := range []string{"sum(node_load1)", "rate(node_b[1m])", "node_a + node_b"} {
		_, _, err := n.Get(sel)
		if err == nil {
			t.Errorf("Get(%q): want an error, aggregation is not allowed", sel)
			continue
		}
		if !strings.Contains(err.Error(), sel) {
			t.Errorf("error must name the selector %q: %v", sel, err)
		}
	}
	if len(q.asked) != 0 {
		t.Errorf("a malformed selector must be rejected before any query, got %v", q.asked)
	}
}

// Missing data is the student's problem, not an error: a typo, an absent
// collector and a scrape gap all read as empty.
func TestGetMissingIsEmptyNotError(t *testing.T) {
	n := NewNode(&fakeQuerier{answers: map[string]signals.Vector{}}, time.Unix(1000, 0), 10*time.Second)
	now, prev, err := n.Get(`does_not_exist{mode="x"}`)
	if err != nil {
		t.Fatalf("Get on a missing series: %v, want nil error", err)
	}
	if len(now) != 0 || len(prev) != 0 {
		t.Errorf("got now=%v prev=%v, want both empty", now, prev)
	}
}

func TestGetPrevIsValueAtTMinusStep(t *testing.T) {
	q := timedQuerier{990: 100, 1000: 250}
	n := NewNode(q, time.Unix(1000, 0), 10*time.Second)
	now, prev, err := n.Get("node_vmstat_pgfault")
	if err != nil {
		t.Fatal(err)
	}
	if now.Sum() != 250 || prev.Sum() != 100 {
		t.Errorf("now=%v prev=%v, want 250 and 100", now.Sum(), prev.Sum())
	}
}

func TestGetBeforeRefreshErrors(t *testing.T) {
	var n Node
	if _, _, err := n.Get("node_load1"); err == nil {
		t.Error("a Node with no querier must say so, not return empty data")
	}
}

func TestLibraryQueriesKeepNodeFilter(t *testing.T) {
	q := &fakeQuerier{answers: map[string]signals.Vector{}}
	inf := NewPromInformer(q, NewStepClock(time.Unix(1, 0), 10*time.Second), "sut1", 10*time.Second)
	if err := inf.Refresh(); err != nil {
		t.Fatal(err)
	}
	// Online mode runs as a DaemonSet on multi-node clusters; unfiltered, every
	// replica would add up every node.
	for _, e := range q.asked {
		if !strings.Contains(e, `node="sut1"`) {
			t.Errorf("library query without node filter: %s", e)
		}
	}
}

// cAdvisor exports one row per container plus one for the pod cgroup
// (container=""), the sum of its containers. Keying by id counted both,
// so every pod's CPU was doubled.
func TestPromInformerExcludesPodLevelCgroupRows(t *testing.T) {
	q := &fakeQuerier{answers: map[string]signals.Vector{}}
	inf := NewPromInformer(q, NewStepClock(time.Unix(1788950000, 0), 10*time.Second), "sut1", 10*time.Second)
	if err := inf.Refresh(); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	for _, expr := range q.asked {
		if strings.Contains(expr, "container_cpu_usage_seconds_total") && !strings.Contains(expr, `container!=""`) {
			t.Fatalf("pod query does not exclude pod-level cgroup rows: %s", expr)
		}
	}
}

// cAdvisor's id label is the container cgroup path, so a multi-container pod
// arrived as one Pod per container: duplicate {pod_namespace,pod_name} series
// live, and cgroup-path keys in pred_pod_w offline. A pod is one entry keyed
// namespace/pod, its containers summed, and the query asks for exactly that.
func TestPromInformerKeysPodsByNamespaceAndName(t *testing.T) {
	q := &fakeQuerier{answers: map[string]signals.Vector{
		"container_cpu_usage": {
			{Labels: map[string]string{"pod": "a", "namespace": "demo", "id": "/kubepods/pod-a/app"}, Value: 3.0},
			{Labels: map[string]string{"pod": "a", "namespace": "demo", "id": "/kubepods/pod-a/sidecar"}, Value: 1.0},
		},
	}}
	inf := NewPromInformer(q, NewStepClock(time.Unix(1000, 0), 10*time.Second), "sut1", 10*time.Second)
	if err := inf.Refresh(); err != nil {
		t.Fatal(err)
	}
	pods := inf.Pods().Running
	if len(pods) != 1 {
		t.Fatalf("got %d pods, want 1: %v", len(pods), pods)
	}
	p, ok := pods["demo/a"]
	if !ok {
		t.Fatalf("pod not keyed namespace/pod: %v", pods)
	}
	if p.ID != "demo/a" || p.CPUTimeDelta != 40 {
		t.Errorf("pod = %+v, want ID demo/a and CPUTimeDelta 40 (3+1 cores x 10 s)", p)
	}
	saw := false
	for _, expr := range q.asked {
		if strings.Contains(expr, "container_cpu_usage_seconds_total") {
			saw = true
			if !strings.Contains(expr, "sum by (pod, namespace)") {
				t.Errorf("pod query still groups by container id: %s", expr)
			}
		}
	}
	if !saw {
		t.Fatal("no pod query was issued")
	}
}
