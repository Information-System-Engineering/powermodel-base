// Package device mirrors kepler/internal/device by shape, not by import.
// A model that works here migrates upstream as one case in buildCPUMeter.
package device

import "time"

// Energy is accumulated energy in microjoules, matching Kepler's unit.
type Energy uint64

// Power is instantaneous power in watts.
type Power float64

// Zone is a RAPL-style measurement domain: package, core, dram, uncore, psys.
type Zone string

const (
	Microjoule Energy = 1
	Joule      Energy = 1_000_000
)

// zonePriority ranks zones by energy coverage, as Kepler's
// PrimaryEnergyZone documents: Psys > Package > Core > DRAM > Uncore.
var zonePriority = map[Zone]int{
	"psys": 0, "package": 1, "core": 2, "dram": 3, "uncore": 4,
}

// microjoules converts watts held for d into microjoules.
func microjoules(w Power, d time.Duration) Energy {
	if w < 0 {
		return 0
	}
	return Energy(float64(w) * d.Seconds() * float64(Joule))
}
