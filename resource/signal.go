package resource

import (
	"fmt"
	"regexp"
	"strings"
)

// rawSignal is a Prometheus metric name with optional label matchers, and
// nothing else. No functions, no ranges, no operators. A label value that
// contains a closing brace is rejected too.
var rawSignal = regexp.MustCompile(`^[a-zA-Z_:][a-zA-Z0-9_:]*(\{[^}]*\})?$`)

// ValidateSignal rejects anything that is not a raw metric selector.
//
// This restriction is the point of the project, not a limitation of it.
// Kepler reads /proc and does its own arithmetic; it has no PromQL. A model
// fitted on PromQL aggregates cannot migrate upstream, so the aggregation has
// to live in the model, where the Kepler PR would put it.
func ValidateSignal(s string) error {
	t := strings.TrimSpace(s)
	if t == "" {
		return fmt.Errorf("empty signal: want a metric name, optionally with label matchers")
	}
	if !rawSignal.MatchString(t) {
		return fmt.Errorf("signal %q is not a raw metric: aggregate in Go, not in the query — "+
			"Kepler reads /proc and has no PromQL, so a model built on PromQL cannot migrate upstream. "+
			"Declare the raw series and do the arithmetic in NodeWatts", s)
	}
	return nil
}
