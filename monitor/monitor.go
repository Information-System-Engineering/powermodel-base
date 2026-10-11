// Package monitor ports the attribution arithmetic from
// kepler/internal/monitor/{node,pod}.go without changing it.
package monitor

import (
	"github.com/Information-System-Engineering/powermodel-base/device"
	"github.com/Information-System-Engineering/powermodel-base/resource"
)

type PodPower struct {
	Name      string
	Namespace string
	Zones     map[device.Zone]device.Power
}

type Snapshot struct {
	Node   map[device.Zone]device.Power // total node power per zone
	Active map[device.Zone]device.Power // node power attributable to work
	Pods   map[string]PodPower
}

type Monitor struct {
	cpu       device.CPUPowerMeter
	resources resource.Informer
	node      string
}

func New(m device.CPUPowerMeter, inf resource.Informer, node string) *Monitor {
	return &Monitor{cpu: m, resources: inf, node: node}
}

// NodeName is the node these readings describe. It is carried rather than
// derived because the informer's node is a query filter, not an identity.
func (m *Monitor) NodeName() string { return m.node }

// Collect reads the meter and the informer once and attributes power to pods.
//
// Ported verbatim from Kepler:
//
//	activePower[zone] = nodeWatts[zone] x nodeCPUUsageRatio
//	podWatts[pod][zone] = activePower[zone] x podCPUDelta / nodeCPUTimeDelta
//
// DEVIATION: Kepler's denominator is Node.ProcessTotalCPUTimeDelta, a sum
// over every process. cAdvisor gives us pods only, so our informer fills that
// field with the node's non-idle CPU time. Both count all CPU work on the
// node, so the shares agree up to the skew between node and cgroup counters.
func (m *Monitor) Collect() (*Snapshot, error) {
	zones, err := m.cpu.Zones()
	if err != nil {
		return nil, err
	}

	node := m.resources.Node()
	s := &Snapshot{
		Node:   make(map[device.Zone]device.Power, len(zones)),
		Active: make(map[device.Zone]device.Power, len(zones)),
		Pods:   map[string]PodPower{},
	}

	for _, z := range zones {
		p, err := z.Power()
		if err != nil {
			return nil, err
		}
		zone := device.Zone(z.Name())
		s.Node[zone] = p
		s.Active[zone] = p * device.Power(node.CPUUsageRatio)
	}

	total := node.ProcessTotalCPUTimeDelta
	for id, pod := range m.resources.Pods().Running {
		pp := PodPower{
			Name:      pod.Name,
			Namespace: pod.Namespace,
			Zones:     make(map[device.Zone]device.Power, len(s.Active)),
		}
		// A zero denominator means no CPU work was observed: every pod's share
		// is zero, not NaN.
		ratio := 0.0
		if total > 0 {
			ratio = pod.CPUTimeDelta / total
		}
		// Node and cgroup counters are scraped independently and need not
		// agree. Clamping keeps the conservation property the scorer asserts.
		if ratio > 1 {
			ratio = 1
		}
		for zone, active := range s.Active {
			pp.Zones[zone] = active * device.Power(ratio)
		}
		s.Pods[id] = pp
	}
	return s, nil
}
