package device

import (
	"sync"
	"time"
)

// Integrator turns a series of watt readings into the wrapping joule counter
// that EnergyZone.Energy() must expose. RAPL counters wrap at MaxEnergy and
// Kepler's monitor is written to expect that, so a model-backed zone has to
// wrap too or the delta arithmetic upstream reads a negative jump as a reset.
type Integrator struct {
	mu    sync.Mutex
	total Energy
	max   Energy
}

func NewIntegrator(max Energy) *Integrator {
	return &Integrator{max: max}
}

// Add accumulates w watts held for d and returns the new counter value.
func (i *Integrator) Add(w Power, d time.Duration) Energy {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.total += microjoules(w, d)
	if i.max > 0 {
		i.total %= i.max
	}
	return i.total
}
