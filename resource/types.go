// Package resource mirrors kepler/internal/resource by shape. Kepler's real
// implementation reads /proc and cgroups; ours reads Prometheus, so a snapshot
// replay and a live cluster drive identical code.
package resource

import (
	"fmt"
	"time"

	"github.com/Information-System-Engineering/powermodel-base/signals"
)

// Node carries what Kepler's monitor reads per refresh.
//
// The first two fields are Kepler's resource.Node verbatim. Everything else a
// model wants it reads itself through Get: raw series, no PromQL arithmetic,
// the way Kepler's procfs reader reads raw counters and takes its own deltas.
type Node struct {
	ProcessTotalCPUTimeDelta float64 // seconds of non-idle CPU over the last step
	CPUUsageRatio            float64 // non-idle fraction, 0..1
	StepSeconds              float64
	// Name is the node label this Node describes. Get does not filter by it:
	// a snapshot can hold several nodes, so put it in your selectors.
	Name string

	q    Querier
	t    time.Time
	step time.Duration
}

// NewNode binds a Node to the querier and the step it reads at.
func NewNode(q Querier, t time.Time, step time.Duration) *Node {
	return &Node{q: q, t: t, step: step, StepSeconds: step.Seconds()}
}

// Get returns the samples of a raw selector at this step and at the previous
// one. The selector is a metric name with optional label matchers and nothing
// else: aggregation belongs in the model, where a Kepler PR would put it.
//
// A selector that matches nothing returns empty vectors and no error. A typo,
// a collector that is not running and a scrape gap all look the same, and
// telling them apart is part of the exercise. err is only for a malformed
// selector or a Prometheus that did not answer.
//
// prev is the value at t-step, not state carried from the last refresh, so no
// mode can lose it.
func (n *Node) Get(selector string) (now, prev signals.Vector, err error) {
	if err := ValidateSignal(selector); err != nil {
		return nil, nil, err
	}
	if n.q == nil {
		return nil, nil, fmt.Errorf("get %q: node was not built by an informer", selector)
	}
	if now, err = n.q.Query(selector, n.t); err != nil {
		return nil, nil, fmt.Errorf("get %q: %w", selector, err)
	}
	if prev, err = n.q.Query(selector, n.t.Add(-n.step)); err != nil {
		return nil, nil, fmt.Errorf("get %q: %w", selector, err)
	}
	return now, prev, nil
}

type Pod struct {
	ID           string
	Name         string
	Namespace    string
	CPUTimeDelta float64 // seconds over the last step
}

type Pods struct {
	Running    map[string]*Pod
	Terminated map[string]*Pod
}

// Clock is Kepler's clock injection point, minus the k8s.io/utils dependency.
// Live mode uses the wall clock; replay steps a simulated one, which is what
// lets Refresh() keep Kepler's argument-free signature.
type Clock interface {
	Now() time.Time
}

type wallClock struct{}

func (wallClock) Now() time.Time { return time.Now() }

// WallClock is the live-mode clock.
func WallClock() Clock { return wallClock{} }

// StepClock walks a replay window one step at a time.
type StepClock struct {
	now  time.Time
	step time.Duration
}

func NewStepClock(start time.Time, step time.Duration) *StepClock {
	return &StepClock{now: start, step: step}
}

func (c *StepClock) Now() time.Time { return c.now }

// Advance moves to the next step and returns the new time.
func (c *StepClock) Advance() time.Time {
	c.now = c.now.Add(c.step)
	return c.now
}
