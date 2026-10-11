package main

import (
	"strings"
	"testing"

	"github.com/Information-System-Engineering/powermodel-base/signals"
)

func TestRefusesSyntheticTruth(t *testing.T) {
	v := signals.Vector{
		{Labels: map[string]string{"node_name": "n1", "zone": "package"}, Value: 50},
		{Labels: map[string]string{"node_name": "n1", "zone": "dram", "synthetic": "true"}, Value: 9},
	}
	err := checkNotSynthetic("kepler_node_cpu_watts", v)
	if err == nil {
		t.Fatal("want a refusal for synthetic energy series")
	}
	if !strings.Contains(err.Error(), "kepler_node_cpu_watts") {
		t.Errorf("error must name the metric, got %q", err)
	}
	if !strings.Contains(err.Error(), "synthetic") {
		t.Errorf("error must say why, got %q", err)
	}
}

func TestAcceptsRealTruth(t *testing.T) {
	v := signals.Vector{
		{Labels: map[string]string{"node_name": "n1", "zone": "package"}, Value: 7.2},
	}
	if err := checkNotSynthetic("kepler_node_cpu_watts", v); err != nil {
		t.Errorf("want nil for measured series, got %v", err)
	}
}
