package resource

import (
	"strings"
	"testing"
)

func TestValidateSignalAccepts(t *testing.T) {
	for _, s := range []string{
		"node_load1",
		"node_vmstat_pgfault",
		`node_cpu_seconds_total{mode!="idle"}`,
		`container_cpu_usage_seconds_total{pod!="",namespace="demo"}`,
		`node_cpu_seconds_total{mode=~"user|system"}`,
		"some:recording:rule",
	} {
		if err := ValidateSignal(s); err != nil {
			t.Errorf("ValidateSignal(%q) = %v, want nil", s, err)
		}
	}
}

func TestValidateSignalRejects(t *testing.T) {
	for _, s := range []string{
		"sum(node_load1)",
		"rate(node_vmstat_pgfault[1m])",
		"1 - node_load1",
		"node_a / node_b",
		`sum(rate(node_cpu_seconds_total{mode!="idle"}[1m]))`,
		"",
		"   ",
		"123_starts_with_digit",
	} {
		if err := ValidateSignal(s); err == nil {
			t.Errorf("ValidateSignal(%q) = nil, want an error", s)
		}
	}
}

func TestValidateSignalErrorTeaches(t *testing.T) {
	// The error has to say what to do instead, or students will try to escape
	// the restriction rather than understand it.
	err := ValidateSignal("sum(node_load1)")
	if err == nil {
		t.Fatal("want an error")
	}
	msg := err.Error()
	if !strings.Contains(msg, "sum(node_load1)") {
		t.Errorf("error must name the signal, got %q", msg)
	}
	if !strings.Contains(msg, "Kepler") {
		t.Errorf("error must explain why, got %q", msg)
	}
}
