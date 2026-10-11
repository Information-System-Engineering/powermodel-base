package monitor

import (
	"testing"
	"time"

	"github.com/Information-System-Engineering/powermodel-base/device"
	"github.com/Information-System-Engineering/powermodel-base/resource"
)

// The numbers below are Kepler's own test fixture values, reproduced from
// kepler/internal/monitor/mock_utils.go (nodeCpuUsage 0.5, nodeCpuTimeDelta
// 200.0) and pod_power_test.go (pod-1 CPUTimeDelta 30, pod-2 20). If this test
// drifts from Kepler's arithmetic, the port has diverged.
func TestAttributionMatchesKepler(t *testing.T) {
	meter := device.NewModelMeter(func() (map[device.Zone]device.Power, error) {
		return map[device.Zone]device.Power{"package": 100}, nil
	}, 10*time.Second)
	meter.Refresh()

	inf := &fakeInformer{
		node: &resource.Node{CPUUsageRatio: 0.5, ProcessTotalCPUTimeDelta: 200.0},
		pods: &resource.Pods{Running: map[string]*resource.Pod{
			"pod-1": {ID: "pod-1", Name: "test-pod-1", Namespace: "default", CPUTimeDelta: 30.0},
			"pod-2": {ID: "pod-2", Name: "test-pod-2", Namespace: "default", CPUTimeDelta: 20.0},
		}},
	}

	s, err := New(meter, inf, "sut1").Collect()
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if s.Node["package"] != 100 {
		t.Fatalf("node power: got %v, want 100", s.Node["package"])
	}
	// active = node x cpuUsageRatio = 100 x 0.5
	if s.Active["package"] != 50 {
		t.Fatalf("active power: got %v, want 50", s.Active["package"])
	}
	// pod = active x podCPUDelta / nodeCPUTimeDelta = 50 x 30/200
	if got := s.Pods["pod-1"].Zones["package"]; got != 7.5 {
		t.Fatalf("pod-1: got %v, want 7.5", got)
	}
	if got := s.Pods["pod-2"].Zones["package"]; got != 5.0 {
		t.Fatalf("pod-2: got %v, want 5", got)
	}
}

func TestPodPowerNeverExceedsNodeActivePower(t *testing.T) {
	meter := device.NewModelMeter(func() (map[device.Zone]device.Power, error) {
		return map[device.Zone]device.Power{"package": 100}, nil
	}, 10*time.Second)
	meter.Refresh()

	// Pods claiming more CPU than the node observed: cgroup and node counters
	// are scraped independently and need not agree.
	inf := &fakeInformer{
		node: &resource.Node{CPUUsageRatio: 1.0, ProcessTotalCPUTimeDelta: 10.0},
		pods: &resource.Pods{Running: map[string]*resource.Pod{
			"hog": {ID: "hog", Name: "hog", CPUTimeDelta: 999.0},
		}},
	}

	s, _ := New(meter, inf, "sut1").Collect()
	var total device.Power
	for _, p := range s.Pods {
		total += p.Zones["package"]
	}
	if total > s.Active["package"] {
		t.Fatalf("conservation violated: pods %v > active %v", total, s.Active["package"])
	}
}

func TestZeroNodeCPUTimeGivesNoPodPower(t *testing.T) {
	meter := device.NewModelMeter(func() (map[device.Zone]device.Power, error) {
		return map[device.Zone]device.Power{"package": 100}, nil
	}, 10*time.Second)
	meter.Refresh()

	inf := &fakeInformer{
		node: &resource.Node{CPUUsageRatio: 0, ProcessTotalCPUTimeDelta: 0},
		pods: &resource.Pods{Running: map[string]*resource.Pod{
			"p": {ID: "p", Name: "p", CPUTimeDelta: 5.0},
		}},
	}
	s, err := New(meter, inf, "sut1").Collect()
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if got := s.Pods["p"].Zones["package"]; got != 0 {
		t.Fatalf("a zero denominator must give zero, not NaN or Inf: got %v", got)
	}
}

type fakeInformer struct {
	node *resource.Node
	pods *resource.Pods
}

func (f *fakeInformer) Init() error          { return nil }
func (f *fakeInformer) Refresh() error       { return nil }
func (f *fakeInformer) Node() *resource.Node { return f.node }
func (f *fakeInformer) Pods() *resource.Pods { return f.pods }
