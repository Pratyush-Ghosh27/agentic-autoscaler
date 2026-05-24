/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

// Package promql constructs PromQL strings for the AgenticAutoscaler
// controller. The PromQL is intentionally hand-written and pinned by tests
// because Grafana panels and alert rules elsewhere in the stack match on
// the same expression — drift here is a silent breaking change.
package promql

import "fmt"

// InstantRPS returns the PromQL for the hot-path instant RPS query (design
// §5 step 2). The 2m rate window matches the Forecast Service's training
// window and the Grafana panels in deploy/prom/.
func InstantRPS(deploymentName string) string {
	return fmt.Sprintf(`sum(rate(http_requests_total{deployment="%s"}[2m]))`, deploymentName)
}

// RangeRPS returns the PromQL for the range RPS query. The expression is
// identical to InstantRPS; the start/end/step parameters that turn it into
// a range query are URL parameters managed by the Prometheus adapter.
func RangeRPS(deploymentName string) string {
	return InstantRPS(deploymentName)
}
