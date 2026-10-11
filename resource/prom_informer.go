package resource

import (
	"fmt"
	"time"

	"github.com/Information-System-Engineering/powermodel-base/signals"
)

// Querier is the read side of signals.Cache and signals.Client, narrowed so
// tests can fake it.
type Querier interface {
	Query(expr string, t time.Time) (signals.Vector, error)
}

// PromInformer stands in for Kepler's procfs reader. Same outputs, different
// source: node-exporter for the node, cAdvisor for pods, and Node.Get for
// whatever else a model reads.
type PromInformer struct {
	q    Querier
	clk  Clock
	node string
	step time.Duration

	node_ *Node
	pods  *Pods
}

var _ Informer = (*PromInformer)(nil)

// NewPromInformer takes the step from the caller: the same value drives the
// cache grid, the clock and the CPU-seconds-per-step deltas, and must.
func NewPromInformer(q Querier, clk Clock, node string, step time.Duration) *PromInformer {
	n := NewNode(q, clk.Now(), step)
	n.Name = node
	return &PromInformer{
		q: q, clk: clk, node: node, step: step,
		node_: n,
		pods:  &Pods{Running: map[string]*Pod{}, Terminated: map[string]*Pod{}},
	}
}

func (p *PromInformer) Name() string { return "prometheus" }

// Init has nothing to check. A selector is validated when the model calls
// Get, and one that matches nothing is the model's to notice, deliberately.
func (p *PromInformer) Init() error { return nil }

// first returns the single value of a scalar-shaped query, or 0 if absent.
func (p *PromInformer) first(expr string, t time.Time) (float64, error) {
	v, err := p.q.Query(expr, t)
	if err != nil {
		return 0, err
	}
	if len(v) == 0 {
		return 0, nil
	}
	return v[0].Value, nil
}

func (p *PromInformer) Refresh() error {
	t := p.clk.Now()
	secs := p.step.Seconds()

	// Busy cores and total cores, both from node-exporter. Filtered by node:
	// online mode runs as a DaemonSet, and each replica must read its own node
	// only.
	//
	// Unlike Node.Get, this uses PromQL: a one-minute rate, so CPUUsageRatio
	// is a trailing one-minute average rather than the last step's delta.
	busy, err := p.first(fmt.Sprintf(
		`sum(rate(node_cpu_seconds_total{mode!="idle",node=%q}[1m]))`, p.node), t)
	if err != nil {
		return err
	}
	cores, err := p.first(fmt.Sprintf(
		`count(node_cpu_seconds_total{mode="idle",node=%q})`, p.node), t)
	if err != nil {
		return err
	}

	ratio := 0.0
	if cores > 0 {
		ratio = busy / cores
		if ratio > 1 {
			ratio = 1
		}
	}
	n := NewNode(p.q, t, p.step)
	n.Name = p.node
	n.CPUUsageRatio = ratio
	// Kepler's denominator is the sum over all processes. We observe pods only,
	// so the node's non-idle CPU time is the closest observable equivalent.
	// Asserted in monitor's test.
	n.ProcessTotalCPUTimeDelta = busy * secs
	p.node_ = n

	// One row per pod, containers summed. cAdvisor's id label is the container
	// cgroup path; grouping by it made a multi-container pod N pods.
	pods, err := p.q.Query(fmt.Sprintf(
		`sum by (pod, namespace) (rate(container_cpu_usage_seconds_total{pod!="",container!="",node=%q}[1m]))`, p.node), t)
	if err != nil {
		return err
	}
	running := make(map[string]*Pod, len(pods))
	for _, s := range pods {
		id := s.Labels["namespace"] + "/" + s.Labels["pod"]
		if pod, ok := running[id]; ok {
			pod.CPUTimeDelta += s.Value * secs // a querier that did not sum for us
			continue
		}
		running[id] = &Pod{
			ID:           id,
			Name:         s.Labels["pod"],
			Namespace:    s.Labels["namespace"],
			CPUTimeDelta: s.Value * secs,
		}
	}
	p.pods.Running = running
	return nil
}

func (p *PromInformer) Node() *Node { return p.node_ }
func (p *PromInformer) Pods() *Pods { return p.pods }
