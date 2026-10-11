package device

import "fmt"

// EnergyZone mirrors kepler/internal/device.EnergyZone exactly.
type EnergyZone interface {
	Name() string
	Index() int
	Path() string
	Energy() (Energy, error)
	MaxEnergy() Energy
	Power() (Power, error)
}

// CPUPowerMeter mirrors kepler/internal/device.CPUPowerMeter. Kepler's version
// embeds PowerMeter for Name() and Init(); we inline both.
type CPUPowerMeter interface {
	Name() string
	Init() error
	Zones() ([]EnergyZone, error)
	PrimaryEnergyZone() (EnergyZone, error)
}

// pickPrimary implements Kepler's documented priority:
// Psys > Package > Core > DRAM > Uncore. Unknown zones rank last.
func pickPrimary(zones []EnergyZone) (EnergyZone, error) {
	if len(zones) == 0 {
		return nil, fmt.Errorf("no zones")
	}
	best, bestRank := zones[0], 1<<30
	for _, z := range zones {
		rank, known := zonePriority[Zone(z.Name())]
		if !known {
			rank = 1 << 20
		}
		if rank < bestRank {
			best, bestRank = z, rank
		}
	}
	return best, nil
}
