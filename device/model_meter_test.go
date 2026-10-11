package device

import (
	"testing"
	"time"
)

func TestModelMeterExposesZonesFromModelOutput(t *testing.T) {
	m := NewModelMeter(func() (map[Zone]Power, error) {
		return map[Zone]Power{"package": 85, "dram": 15}, nil
	}, 10*time.Second)

	if err := m.Refresh(); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	zones, err := m.Zones()
	if err != nil {
		t.Fatalf("Zones: %v", err)
	}
	if len(zones) != 2 {
		t.Fatalf("got %d zones, want 2", len(zones))
	}

	byName := map[string]EnergyZone{}
	for _, z := range zones {
		byName[z.Name()] = z
	}
	if p, _ := byName["package"].Power(); p != 85 {
		t.Fatalf("package power: got %v, want 85", p)
	}
	// 85 W for 10 s = 850 J
	if e, _ := byName["package"].Energy(); e != 850*Joule {
		t.Fatalf("package energy after one step: got %d, want %d", e, 850*Joule)
	}
}

func TestModelMeterEnergyClimbsAcrossRefreshes(t *testing.T) {
	m := NewModelMeter(func() (map[Zone]Power, error) {
		return map[Zone]Power{"package": 10}, nil
	}, 2*time.Second)

	m.Refresh()
	m.Refresh()
	zones, _ := m.Zones()
	// 10 W x 2 s, twice = 40 J
	if e, _ := zones[0].Energy(); e != 40*Joule {
		t.Fatalf("counter must climb across refreshes: got %d, want %d", e, 40*Joule)
	}
}

func TestModelMeterPrimaryZoneFollowsKeplerPriority(t *testing.T) {
	m := NewModelMeter(func() (map[Zone]Power, error) {
		return map[Zone]Power{"dram": 15, "package": 85, "uncore": 5}, nil
	}, time.Second)
	m.Refresh()

	z, err := m.PrimaryEnergyZone()
	if err != nil {
		t.Fatalf("PrimaryEnergyZone: %v", err)
	}
	if z.Name() != "package" {
		t.Fatalf("got %q, want package (Psys > Package > Core > DRAM > Uncore)", z.Name())
	}
}

func TestModelMeterDropsAZoneTheModelLeftOut(t *testing.T) {
	out := map[Zone]Power{"package": 85, "dram": 15}
	m := NewModelMeter(func() (map[Zone]Power, error) { return out, nil }, time.Second)
	m.Refresh()
	out = map[Zone]Power{"package": 90}
	m.Refresh()
	zones, _ := m.Zones()
	if len(zones) != 1 || zones[0].Name() != "package" {
		t.Fatalf("zones %v, want only package: dram was not estimated this step", names(zones))
	}
}

func TestModelMeterSurfacesModelError(t *testing.T) {
	m := NewModelMeter(func() (map[Zone]Power, error) {
		return nil, errBoom
	}, time.Second)
	if err := m.Refresh(); err == nil {
		t.Fatal("a failing model must fail the refresh, not emit zero watts")
	}
}

var errBoom = errString("model exploded")

type errString string

func (e errString) Error() string { return string(e) }

func names(zones []EnergyZone) []string {
	out := make([]string, len(zones))
	for i, z := range zones {
		out[i] = z.Name()
	}
	return out
}
