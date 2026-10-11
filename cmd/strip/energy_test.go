package main

import (
	"regexp"
	"testing"
)

func TestEnergyNamesCoverPowerSources(t *testing.T) {
	// Prometheus anchors =~ at both ends.
	re := regexp.MustCompile(`^(?:` + energyNames + `)$`)
	for _, name := range []string{
		"kepler_node_cpu_watts", "kepler_pod_cpu_joules_total",
		"scaph_host_power_microwatts", "node_rapl_package_joules_total",
		"node_hwmon_power_average_watt", "node_hwmon_curr_amps", "node_hwmon_in_volts",
		"ipmi_dcmi_power_consumption_watts", "redfish_power_consumed_watts",
		"node_power_supply_power_watt",
	} {
		if !re.MatchString(name) {
			t.Errorf("%s survives the strip", name)
		}
	}
	for _, name := range []string{
		"node_cpu_seconds_total", "container_cpu_usage_seconds_total",
		"node_hwmon_temp_celsius", "node_cpu_scaling_frequency_hertz", "node_vmstat_pgfault",
	} {
		if re.MatchString(name) {
			t.Errorf("%s is an ordinary signal and must not be deleted", name)
		}
	}
}
