package device

import (
	"fmt"
	"slices"
	"sync"
	"time"
)

// NodeWattsFunc is the model call, already bound to its inputs. The device
// package must not know what a model is or where its signals come from.
type NodeWattsFunc func() (map[Zone]Power, error)

// maxModelEnergy is the wrap point for model-backed counters. RAPL's own
// max is hardware-defined; any large value works as long as Energy() wraps,
// because that is the behaviour Kepler's delta arithmetic is written against.
const maxModelEnergy = Energy(1 << 60)

// ModelMeter is the deliverable: a CPUPowerMeter whose readings come from a
// model rather than from silicon. Migrating upstream means adding one
// case "model": to Kepler's buildCPUMeter that constructs this.
type ModelMeter struct {
	f    NodeWattsFunc
	step time.Duration

	mu    sync.Mutex
	gen   uint64 // counts refreshes
	zones map[Zone]*modelZone
	order []Zone
}

var _ CPUPowerMeter = (*ModelMeter)(nil)

func NewModelMeter(f NodeWattsFunc, step time.Duration) *ModelMeter {
	return &ModelMeter{f: f, step: step, zones: map[Zone]*modelZone{}}
}

func (m *ModelMeter) Name() string { return "model" }
func (m *ModelMeter) Init() error  { return nil }

// Refresh calls the model once. A zone the model leaves out of this call is
// a gap, not its previous value: Zones() omits it until the model reports it
// again. Its energy counter and index are kept for when it comes back.
func (m *ModelMeter) Refresh() error {
	watts, err := m.f()
	if err != nil {
		// A model that fails must fail the read. Emitting zero watts would be
		// a number pretending to be an estimate.
		return fmt.Errorf("model: %w", err)
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	m.gen++
	for zone, w := range watts {
		z, ok := m.zones[zone]
		if !ok {
			z = &modelZone{name: string(zone), integ: NewIntegrator(maxModelEnergy)}
			m.zones[zone] = z
			m.order = append(m.order, zone)
			slices.Sort(m.order)
			for i, name := range m.order {
				m.zones[name].index = i
			}
		}
		z.set(w, m.step)
		z.gen = m.gen
	}
	return nil
}

// Zones returns the zones the latest refresh reported, in name order.
func (m *ModelMeter) Zones() ([]EnergyZone, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]EnergyZone, 0, len(m.order))
	for _, name := range m.order {
		if z := m.zones[name]; z.gen == m.gen {
			out = append(out, z)
		}
	}
	return out, nil
}

func (m *ModelMeter) PrimaryEnergyZone() (EnergyZone, error) {
	zones, err := m.Zones()
	if err != nil {
		return nil, err
	}
	return pickPrimary(zones)
}

type modelZone struct {
	name  string
	index int
	integ *Integrator
	gen   uint64 // the ModelMeter refresh that last reported this zone

	mu     sync.Mutex
	watts  Power
	joules Energy
}

var _ EnergyZone = (*modelZone)(nil)

func (z *modelZone) set(w Power, d time.Duration) {
	z.mu.Lock()
	defer z.mu.Unlock()
	z.watts = w
	z.joules = z.integ.Add(w, d)
}

func (z *modelZone) Name() string { return z.name }
func (z *modelZone) Index() int   { return z.index }

// Path names the source rather than a sysfs node, so anything reading Path()
// cannot mistake an estimate for a hardware reading.
func (z *modelZone) Path() string { return "model://" + z.name }

func (z *modelZone) Energy() (Energy, error) {
	z.mu.Lock()
	defer z.mu.Unlock()
	return z.joules, nil
}

func (z *modelZone) MaxEnergy() Energy { return maxModelEnergy }

func (z *modelZone) Power() (Power, error) {
	z.mu.Lock()
	defer z.mu.Unlock()
	return z.watts, nil
}
