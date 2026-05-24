// Shared dual-target URL construction for k6 scenarios.
// Both deployments (app-agentic, app-hpa) receive byte-identical request
// streams so any difference in tail latency / 503 rate is attributable
// solely to the autoscaler under test, not to the load profile.
//
// All scenarios import getTargets() and workURL() from this module —
// renaming an export here is a breaking change for the entire k6 suite.

export function getTargets() {
  const agenticURL = __ENV.TARGET_AGENTIC_URL || "http://localhost:8080";
  const hpaURL = __ENV.TARGET_HPA_URL || "http://localhost:8081";
  return { agentic: agenticURL, hpa: hpaURL };
}

export function workURL(baseURL) {
  return `${baseURL}/work`;
}
