# Plan 02 — Validating Admission Webhook Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implement the validating admission webhook for the `AgenticAutoscaler` CRD that rejects `Create` and `Update` requests violating the bound checks listed in `docs/design.md` §4. Wire it through cert-manager for TLS, register it on the controller-runtime manager, and exercise every rule via envtest plus a kubectl-apply smoke check.

**Architecture:** A single `Validator` type implementing controller-runtime's `webhook.CustomValidator` interface (the v4 kubebuilder pattern). Validation logic lives in `internal/webhook/validator.go` as a stateless function `validateSpec(*AgenticAutoscaler) error` that the `CustomValidator` methods call from both `ValidateCreate` and `ValidateUpdate`. Cert-manager is the only TLS issuer in scope — no manual cert wiring. The webhook handler is registered on the manager in `cmd/controller/main.go`.

**Tech Stack:** Go 1.23, controller-runtime v0.19 webhook package, kubebuilder v4 webhook scaffold, cert-manager v1.16, testify, envtest, ginkgo/v2 + gomega (continued from Plan #1's scaffold).

---

## Spec Coverage Map

Each row in design §4's "Admission webhook" list maps to a task. Optional fields fire validation only when non-nil.

| Bound check from design §4 | Task |
| --- | --- |
| `minReplicas < 1` rejected | T3 |
| `maxReplicas < minReplicas` rejected | T3 |
| `rpsPerPodMin < 1` rejected | T4 |
| `rpsPerPodMin >= rpsPerPodMax` rejected | T4 |
| `maxStepSize < 1` (if set) rejected | T5 |
| `maxStepSize > (maxReplicas - minReplicas)` (if set) rejected | T5 |
| `scaleUpCooldownSeconds < 0` (if set) rejected | T6 |
| `scaleDownCooldownSeconds < 0` (if set) rejected | T6 |
| `preferredForecaster` not in {`prophet`, `linear_extrap`, `auto`} rejected | T7 |
| Optional fields: validation only fires when non-nil | T2 (framework), T5/T6/T7 (per-rule) |
| cert-manager TLS for the webhook | T8 |
| Webhook URL wired by kubebuilder scaffolding | T1 |

What's intentionally not in this plan: any logic that depends on Prometheus availability, classifier output, or the reconciler. The webhook is a pure shape-check — it never reads cluster state. Mutating webhooks are explicitly not in scope (none documented in §4).

---

## File Structure

```
scaler/
├── api/v1alpha1/
│   └── agenticautoscaler_webhook.go              # T1: kubebuilder scaffold (CustomValidator wiring)
├── internal/webhook/
│   ├── validator.go                              # T2-T7: pure validateSpec()
│   └── validator_test.go                         # T2-T7: table-driven rule coverage
├── config/                                       # extended by T1, T8
│   ├── certmanager/                              # T8: Issuer + Certificate
│   │   ├── kustomization.yaml
│   │   └── certificate.yaml
│   └── webhook/                                  # T1: kubebuilder generates
│       ├── kustomization.yaml
│       ├── manifests.yaml                        # ValidatingWebhookConfiguration
│       └── service.yaml
├── cmd/controller/main.go                        # T9: register webhook on manager
└── internal/controller/suite_test.go             # T10: enable webhook in envtest harness
```

### File responsibilities

- `internal/webhook/validator.go` — stateless `validateSpec(spec *AgenticAutoscalerSpec) error` returning a `field.ErrorList`-wrapped `error` whose message lists every problem found (so an operator with multiple bad fields sees all of them at once). Plus a tiny `Validator{}` struct implementing `CustomValidator`.
- `api/v1alpha1/agenticautoscaler_webhook.go` — kubebuilder-scaffolded glue that registers the validator with the manager.
- `config/certmanager/` — cert-manager `Issuer` (self-signed) + `Certificate` for the webhook service. Hand-edited so the secret name and DNS SANs match the webhook service in `config/webhook/`.
- `config/webhook/` — kubebuilder generates this; we don't edit by hand.

The test file lives next to `validator.go` (idiomatic Go) so the table covers every rule transition without round-tripping through controller-runtime.

---

## Phase 0 — Webhook scaffold

### Task 1: Generate the kubebuilder webhook scaffold

**Files:**
- Create: `api/v1alpha1/agenticautoscaler_webhook.go`
- Create: `config/webhook/`
- Create: `config/certmanager/`
- Modify: `PROJECT`
- Modify: `cmd/controller/main.go`
- Modify: `config/default/kustomization.yaml`

- [ ] **Step 1: Run kubebuilder create webhook**

```bash
kubebuilder create webhook \
  --group autoscaling \
  --version v1alpha1 \
  --kind AgenticAutoscaler \
  --programmatic-validation
```

Expected: kubebuilder reports new files under `api/v1alpha1/` and `config/webhook/`, edits `cmd/controller/main.go`, and modifies `PROJECT`.

`--programmatic-validation` opts into the `CustomValidator` pattern (preferred over the deprecated `Validator` interface); we do not opt into mutating or defaulting webhooks.

- [ ] **Step 2: Inspect what kubebuilder generated**

Run: `git status` and verify the change set matches the expected file list above.

- [ ] **Step 3: Verify the project still compiles**

Run: `go build ./...`
Expected: clean.

- [ ] **Step 4: Commit**

```bash
git add .
git commit -m "feat(webhook): scaffold validating webhook for AgenticAutoscaler"
```

---

## Phase 1 — Validator logic (Tier-1 strict TDD)

### Task 2: validateSpec scaffold + happy-path test

**Files:**
- Create: `internal/webhook/validator.go`
- Create: `internal/webhook/validator_test.go`
- Modify: `api/v1alpha1/agenticautoscaler_webhook.go` (route to internal/webhook)

- [ ] **Step 1: Write the failing happy-path test FIRST**

Create `internal/webhook/validator_test.go`:

```go
package webhook_test

import (
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	autoscalingv1alpha1 "github.com/pratyush-ghosh/agentic-autoscaler/api/v1alpha1"
	"github.com/pratyush-ghosh/agentic-autoscaler/internal/webhook"
)

// helper: minimal valid CR.
func validCR() *autoscalingv1alpha1.AgenticAutoscaler {
	min := int32(2)
	max := int32(10)
	rpsMin := int32(50)
	rpsMax := int32(500)
	return &autoscalingv1alpha1.AgenticAutoscaler{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "app-agentic",
			Namespace: "demo",
		},
		Spec: autoscalingv1alpha1.AgenticAutoscalerSpec{
			TargetRef: autoscalingv1alpha1.CrossVersionObjectReference{
				APIVersion: "apps/v1",
				Kind:       "Deployment",
				Name:       "app-agentic",
			},
			MinReplicas:  &min,
			MaxReplicas:  &max,
			RpsPerPodMin: &rpsMin,
			RpsPerPodMax: &rpsMax,
		},
	}
}

func TestValidateSpec_HappyPath(t *testing.T) {
	cr := validCR()
	err := webhook.ValidateSpec(&cr.Spec)
	require.NoError(t, err)
	_ = assert.NotNil // satisfy import
}
```

- [ ] **Step 2: Run; expect ImportError**

Run: `go test ./internal/webhook/... -v`
Expected: build failure — `webhook.ValidateSpec` undefined.

- [ ] **Step 3: Implement the minimal validator**

Create `internal/webhook/validator.go`:

```go
// Package webhook hosts the validating admission webhook for the
// AgenticAutoscaler CRD. See docs/design.md §4 for the full bound-check
// list this package implements.
package webhook

import (
	"fmt"
	"strings"

	autoscalingv1alpha1 "github.com/pratyush-ghosh/agentic-autoscaler/api/v1alpha1"
)

// ValidateSpec returns a non-nil error if the spec violates any of the
// bound checks documented in design §4. The error message lists every
// problem found, so an operator sees them all at once instead of fixing
// one and re-applying.
//
// Optional fields (those typed as pointers in v1alpha1) are only checked
// when non-nil. Required fields are assumed satisfied by Pydantic-style
// kubebuilder validation markers and the API server's structural schema.
func ValidateSpec(spec *autoscalingv1alpha1.AgenticAutoscalerSpec) error {
	var problems []string

	// Future tasks T3-T7 append rules below.

	if len(problems) == 0 {
		return nil
	}
	return fmt.Errorf("agenticautoscaler validation failed: %s", strings.Join(problems, "; "))
}
```

- [ ] **Step 4: Run; verify pass**

Run: `go test ./internal/webhook/... -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/webhook/ api/v1alpha1/
git commit -m "feat(webhook): add ValidateSpec scaffold (happy path)"
```

---

### Task 3: Replica-bound rules

**Files:**
- Modify: `internal/webhook/validator.go`
- Modify: `internal/webhook/validator_test.go`

- [ ] **Step 1: Append failing rule tests**

Append to `internal/webhook/validator_test.go`:

```go
func ptr32(v int32) *int32 { return &v }

func TestValidateSpec_RejectsMinReplicasZero(t *testing.T) {
	cr := validCR()
	cr.Spec.MinReplicas = ptr32(0)

	err := webhook.ValidateSpec(&cr.Spec)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "minReplicas")
}

func TestValidateSpec_RejectsMinReplicasNegative(t *testing.T) {
	cr := validCR()
	cr.Spec.MinReplicas = ptr32(-1)

	err := webhook.ValidateSpec(&cr.Spec)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "minReplicas")
}

func TestValidateSpec_RejectsMaxReplicasBelowMin(t *testing.T) {
	cr := validCR()
	cr.Spec.MinReplicas = ptr32(5)
	cr.Spec.MaxReplicas = ptr32(3)

	err := webhook.ValidateSpec(&cr.Spec)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "maxReplicas")
	assert.Contains(t, err.Error(), "minReplicas")
}

func TestValidateSpec_AcceptsMinEqualsMax(t *testing.T) {
	cr := validCR()
	cr.Spec.MinReplicas = ptr32(5)
	cr.Spec.MaxReplicas = ptr32(5)

	err := webhook.ValidateSpec(&cr.Spec)
	require.NoError(t, err, "min == max is allowed (pinned replica count)")
}
```

- [ ] **Step 2: Run; expect failure**

Expected: the three rejection tests FAIL (no rule code yet).

- [ ] **Step 3: Add the replica rule block to validator.go**

Replace the `// Future tasks T3-T7 append rules below.` comment with:

```go
	// Replica bounds — design §4.
	if spec.MinReplicas != nil && *spec.MinReplicas < 1 {
		problems = append(problems, fmt.Sprintf(
			"minReplicas=%d must be >= 1", *spec.MinReplicas))
	}
	if spec.MinReplicas != nil && spec.MaxReplicas != nil &&
		*spec.MaxReplicas < *spec.MinReplicas {
		problems = append(problems, fmt.Sprintf(
			"maxReplicas=%d must be >= minReplicas=%d",
			*spec.MaxReplicas, *spec.MinReplicas))
	}
```

- [ ] **Step 4: Run; verify all pass**

Run: `go test ./internal/webhook/... -v`
Expected: all PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/webhook/
git commit -m "feat(webhook): reject invalid replica bounds"
```

---

### Task 4: Capacity-bound rules

**Files:**
- Modify: `internal/webhook/validator.go`
- Modify: `internal/webhook/validator_test.go`

- [ ] **Step 1: Append failing tests**

```go
func TestValidateSpec_RejectsRpsPerPodMinZero(t *testing.T) {
	cr := validCR()
	cr.Spec.RpsPerPodMin = ptr32(0)
	err := webhook.ValidateSpec(&cr.Spec)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "rpsPerPodMin")
}

func TestValidateSpec_RejectsRpsPerPodMinAboveMax(t *testing.T) {
	cr := validCR()
	cr.Spec.RpsPerPodMin = ptr32(500)
	cr.Spec.RpsPerPodMax = ptr32(500) // equal: must reject (>=)
	err := webhook.ValidateSpec(&cr.Spec)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "rpsPerPodMin")
	assert.Contains(t, err.Error(), "rpsPerPodMax")
}

func TestValidateSpec_AcceptsRpsPerPodMinJustBelowMax(t *testing.T) {
	cr := validCR()
	cr.Spec.RpsPerPodMin = ptr32(499)
	cr.Spec.RpsPerPodMax = ptr32(500)
	err := webhook.ValidateSpec(&cr.Spec)
	require.NoError(t, err)
}
```

- [ ] **Step 2: Run; expect failure**

- [ ] **Step 3: Append capacity rules**

After the replica rules in `validator.go`, add:

```go
	// Capacity bounds — design §4.
	if spec.RpsPerPodMin != nil && *spec.RpsPerPodMin < 1 {
		problems = append(problems, fmt.Sprintf(
			"rpsPerPodMin=%d must be >= 1", *spec.RpsPerPodMin))
	}
	if spec.RpsPerPodMin != nil && spec.RpsPerPodMax != nil &&
		*spec.RpsPerPodMin >= *spec.RpsPerPodMax {
		problems = append(problems, fmt.Sprintf(
			"rpsPerPodMin=%d must be < rpsPerPodMax=%d",
			*spec.RpsPerPodMin, *spec.RpsPerPodMax))
	}
```

- [ ] **Step 4: Run; verify pass**

- [ ] **Step 5: Commit**

```bash
git add internal/webhook/
git commit -m "feat(webhook): reject invalid rpsPerPod capacity bounds"
```

---

### Task 5: maxStepSize rules (only fire when non-nil)

**Files:**
- Modify: `internal/webhook/validator.go`
- Modify: `internal/webhook/validator_test.go`

- [ ] **Step 1: Append failing tests**

```go
func TestValidateSpec_AcceptsMaxStepSizeNil(t *testing.T) {
	cr := validCR()
	cr.Spec.MaxStepSize = nil // operator deferred to classifier
	err := webhook.ValidateSpec(&cr.Spec)
	require.NoError(t, err)
}

func TestValidateSpec_RejectsMaxStepSizeZero(t *testing.T) {
	cr := validCR()
	cr.Spec.MaxStepSize = ptr32(0)
	err := webhook.ValidateSpec(&cr.Spec)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "maxStepSize")
}

func TestValidateSpec_RejectsMaxStepSizeAboveRange(t *testing.T) {
	cr := validCR()
	cr.Spec.MinReplicas = ptr32(2)
	cr.Spec.MaxReplicas = ptr32(5)
	cr.Spec.MaxStepSize = ptr32(4) // 4 > (5 - 2)
	err := webhook.ValidateSpec(&cr.Spec)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "maxStepSize")
	assert.Contains(t, err.Error(), "maxReplicas - minReplicas")
}

func TestValidateSpec_AcceptsMaxStepSizeAtRange(t *testing.T) {
	cr := validCR()
	cr.Spec.MinReplicas = ptr32(2)
	cr.Spec.MaxReplicas = ptr32(5)
	cr.Spec.MaxStepSize = ptr32(3) // exactly (5 - 2)
	err := webhook.ValidateSpec(&cr.Spec)
	require.NoError(t, err)
}
```

- [ ] **Step 2: Run; expect failure**

- [ ] **Step 3: Append maxStepSize rules**

After the capacity rules in `validator.go`, add:

```go
	// maxStepSize bounds — design §4 (only when non-nil).
	if spec.MaxStepSize != nil {
		if *spec.MaxStepSize < 1 {
			problems = append(problems, fmt.Sprintf(
				"maxStepSize=%d must be >= 1", *spec.MaxStepSize))
		}
		if spec.MinReplicas != nil && spec.MaxReplicas != nil {
			rangeSize := *spec.MaxReplicas - *spec.MinReplicas
			if *spec.MaxStepSize > rangeSize {
				problems = append(problems, fmt.Sprintf(
					"maxStepSize=%d must be <= maxReplicas - minReplicas (=%d)",
					*spec.MaxStepSize, rangeSize))
			}
		}
	}
```

- [ ] **Step 4: Run; verify pass**

- [ ] **Step 5: Commit**

```bash
git add internal/webhook/
git commit -m "feat(webhook): reject invalid maxStepSize"
```

---

### Task 6: Cooldown rules

**Files:**
- Modify: `internal/webhook/validator.go`
- Modify: `internal/webhook/validator_test.go`

- [ ] **Step 1: Append failing tests**

```go
func TestValidateSpec_AcceptsCooldownsNil(t *testing.T) {
	cr := validCR()
	cr.Spec.ScaleUpCooldownSeconds = nil
	cr.Spec.ScaleDownCooldownSeconds = nil
	err := webhook.ValidateSpec(&cr.Spec)
	require.NoError(t, err)
}

func TestValidateSpec_AcceptsCooldownsZero(t *testing.T) {
	cr := validCR()
	cr.Spec.ScaleUpCooldownSeconds = ptr32(0)   // zero is allowed
	cr.Spec.ScaleDownCooldownSeconds = ptr32(0)
	err := webhook.ValidateSpec(&cr.Spec)
	require.NoError(t, err)
}

func TestValidateSpec_RejectsNegativeScaleUpCooldown(t *testing.T) {
	cr := validCR()
	cr.Spec.ScaleUpCooldownSeconds = ptr32(-5)
	err := webhook.ValidateSpec(&cr.Spec)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "scaleUpCooldownSeconds")
}

func TestValidateSpec_RejectsNegativeScaleDownCooldown(t *testing.T) {
	cr := validCR()
	cr.Spec.ScaleDownCooldownSeconds = ptr32(-1)
	err := webhook.ValidateSpec(&cr.Spec)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "scaleDownCooldownSeconds")
}
```

- [ ] **Step 2: Run; expect failure**

- [ ] **Step 3: Append cooldown rules**

```go
	// Cooldowns — design §4 (only when non-nil; zero is explicit no-cooldown).
	if spec.ScaleUpCooldownSeconds != nil && *spec.ScaleUpCooldownSeconds < 0 {
		problems = append(problems, fmt.Sprintf(
			"scaleUpCooldownSeconds=%d must be >= 0", *spec.ScaleUpCooldownSeconds))
	}
	if spec.ScaleDownCooldownSeconds != nil && *spec.ScaleDownCooldownSeconds < 0 {
		problems = append(problems, fmt.Sprintf(
			"scaleDownCooldownSeconds=%d must be >= 0", *spec.ScaleDownCooldownSeconds))
	}
```

- [ ] **Step 4: Run; verify pass**

- [ ] **Step 5: Commit**

```bash
git add internal/webhook/
git commit -m "feat(webhook): reject negative cooldowns"
```

---

### Task 7: preferredForecaster enum rule

**Files:**
- Modify: `internal/webhook/validator.go`
- Modify: `internal/webhook/validator_test.go`

- [ ] **Step 1: Append failing tests**

```go
func ptrStr(s string) *string { return &s }

func TestValidateSpec_AcceptsKnownForecasters(t *testing.T) {
	for _, model := range []string{"prophet", "linear_extrap", "auto"} {
		t.Run(model, func(t *testing.T) {
			cr := validCR()
			cr.Spec.PreferredForecaster = ptrStr(model)
			err := webhook.ValidateSpec(&cr.Spec)
			require.NoError(t, err)
		})
	}
}

func TestValidateSpec_RejectsUnknownForecaster(t *testing.T) {
	cr := validCR()
	cr.Spec.PreferredForecaster = ptrStr("xgboost")
	err := webhook.ValidateSpec(&cr.Spec)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "preferredForecaster")
	assert.Contains(t, err.Error(), "xgboost")
}

func TestValidateSpec_AcceptsNilForecaster(t *testing.T) {
	cr := validCR()
	cr.Spec.PreferredForecaster = nil
	err := webhook.ValidateSpec(&cr.Spec)
	require.NoError(t, err)
}
```

- [ ] **Step 2: Run; expect failure**

- [ ] **Step 3: Append the enum rule**

```go
	// preferredForecaster — design §4 (only when non-nil).
	if spec.PreferredForecaster != nil {
		v := *spec.PreferredForecaster
		switch v {
		case "prophet", "linear_extrap", "auto":
			// accepted
		default:
			problems = append(problems, fmt.Sprintf(
				"preferredForecaster=%q must be one of prophet, linear_extrap, auto", v))
		}
	}
```

- [ ] **Step 4: Run; verify pass**

- [ ] **Step 5: Multi-error test (verify the message lists every problem)**

Append:

```go
func TestValidateSpec_AggregatesMultipleProblems(t *testing.T) {
	cr := validCR()
	cr.Spec.MinReplicas = ptr32(0)               // problem 1
	cr.Spec.RpsPerPodMin = ptr32(600)
	cr.Spec.RpsPerPodMax = ptr32(500)            // problem 2 (min >= max)
	cr.Spec.PreferredForecaster = ptrStr("foo")  // problem 3

	err := webhook.ValidateSpec(&cr.Spec)
	require.Error(t, err)
	msg := err.Error()
	assert.Contains(t, msg, "minReplicas")
	assert.Contains(t, msg, "rpsPerPod")
	assert.Contains(t, msg, "preferredForecaster")
}
```

Run: `go test ./internal/webhook/... -v`
Expected: PASS — the validator collects all problems before returning the joined error.

- [ ] **Step 6: Coverage check**

Run: `go test ./internal/webhook/... -cover`
Expected: `>= 95%` on `internal/webhook/`.

- [ ] **Step 7: Commit**

```bash
git add internal/webhook/
git commit -m "feat(webhook): reject unknown preferredForecaster + multi-error aggregation"
```

---

## Phase 2 — cert-manager + webhook wiring

### Task 8: cert-manager Issuer + Certificate

**Files:**
- Create: `config/certmanager/kustomization.yaml`
- Create: `config/certmanager/certificate.yaml`
- Modify: `config/default/kustomization.yaml` (uncomment cert-manager bases)
- Modify: `config/default/manager_webhook_patch.yaml` (already scaffolded by T1)

kubebuilder v4's webhook scaffold leaves cert-manager pieces commented out in `config/default/kustomization.yaml`. We uncomment them and ensure the certificate's DNS SANs match the webhook service.

- [ ] **Step 1: Create config/certmanager/kustomization.yaml**

```yaml
resources:
- certificate.yaml

configurations:
- kustomizeconfig.yaml
```

- [ ] **Step 2: Create config/certmanager/kustomizeconfig.yaml**

```yaml
nameReference:
- kind: Issuer
  group: cert-manager.io
  fieldSpecs:
  - kind: Certificate
    group: cert-manager.io
    path: spec/issuerRef/name
```

- [ ] **Step 3: Create config/certmanager/certificate.yaml**

```yaml
apiVersion: cert-manager.io/v1
kind: Issuer
metadata:
  name: selfsigned-issuer
  namespace: system
spec:
  selfSigned: {}
---
apiVersion: cert-manager.io/v1
kind: Certificate
metadata:
  name: serving-cert
  namespace: system
spec:
  dnsNames:
  - SERVICE_NAME.SERVICE_NAMESPACE.svc
  - SERVICE_NAME.SERVICE_NAMESPACE.svc.cluster.local
  issuerRef:
    kind: Issuer
    name: selfsigned-issuer
  secretName: webhook-server-cert
```

The literals `SERVICE_NAME` and `SERVICE_NAMESPACE` get substituted by kustomize; the kubebuilder default `config/default/kustomization.yaml` configures the substitution. No hand-substitution needed.

- [ ] **Step 4: Enable cert-manager + webhook bases in config/default/kustomization.yaml**

Open `config/default/kustomization.yaml`. Uncomment the lines kubebuilder commented out under the `# [WEBHOOK] / [CERTMANAGER]` markers — typically:

```yaml
bases:
- ../crd
- ../rbac
- ../manager
- ../webhook        # uncomment
- ../certmanager    # uncomment

patchesStrategicMerge:
- manager_webhook_patch.yaml          # uncomment
- webhookcainjection_patch.yaml       # uncomment

vars:
- name: CERTIFICATE_NAMESPACE
  objref:
    kind: Certificate
    group: cert-manager.io
    version: v1
    name: serving-cert
  fieldref:
    fieldpath: metadata.namespace
- name: CERTIFICATE_NAME
  objref:
    kind: Certificate
    group: cert-manager.io
    version: v1
    name: serving-cert
- name: SERVICE_NAMESPACE
  objref:
    kind: Service
    version: v1
    name: webhook-service
  fieldref:
    fieldpath: metadata.namespace
- name: SERVICE_NAME
  objref:
    kind: Service
    version: v1
    name: webhook-service
```

The `vars` block may already be in the file from kubebuilder's scaffold; uncomment instead of duplicating. If kubebuilder generated different names (e.g. `controller-manager-webhook-service`), prefer those.

- [ ] **Step 5: Render the manifest set with kustomize and verify it parses**

```bash
kubectl kustomize config/default | kubeconform - || true
```

If `kubeconform` isn't installed (Plan #11 installs it via `make tools`), use `kubectl --dry-run=client apply -f -`:

```bash
kubectl kustomize config/default | kubectl apply --dry-run=client -f -
```

Expected: every resource validates / dry-runs successfully (assuming the cluster you're targeting has the cert-manager CRDs installed; on a vanilla `kind` cluster it'll complain about `cert-manager.io/v1` which is fine — Plan #10's smoke installs cert-manager).

- [ ] **Step 6: Commit**

```bash
git add config/
git commit -m "feat(webhook): wire cert-manager Issuer + Certificate for webhook TLS"
```

---

### Task 9: Connect ValidateSpec to the kubebuilder CustomValidator

**Files:**
- Modify: `api/v1alpha1/agenticautoscaler_webhook.go`

- [ ] **Step 1: Inspect the file kubebuilder generated in T1**

Open `api/v1alpha1/agenticautoscaler_webhook.go`. It contains a `Validator` type implementing `webhook.CustomValidator` with stub `ValidateCreate`, `ValidateUpdate`, `ValidateDelete` methods. Each currently logs a placeholder.

- [ ] **Step 2: Replace the stubs to call internal/webhook.ValidateSpec**

In the imports, add:

```go
	"github.com/pratyush-ghosh/agentic-autoscaler/internal/webhook"
```

Replace the bodies of `ValidateCreate` and `ValidateUpdate` (keep their signatures kubebuilder generated). The kubebuilder default looks roughly like:

```go
func (v *AgenticAutoscalerCustomValidator) ValidateCreate(ctx context.Context, obj runtime.Object) (admission.Warnings, error) {
    aas, ok := obj.(*AgenticAutoscaler)
    if !ok {
        return nil, fmt.Errorf("expected AgenticAutoscaler, got %T", obj)
    }
    // log statement here
    return nil, nil
}
```

Replace with:

```go
func (v *AgenticAutoscalerCustomValidator) ValidateCreate(ctx context.Context, obj runtime.Object) (admission.Warnings, error) {
    aas, ok := obj.(*AgenticAutoscaler)
    if !ok {
        return nil, fmt.Errorf("expected AgenticAutoscaler, got %T", obj)
    }
    return nil, webhook.ValidateSpec(&aas.Spec)
}

func (v *AgenticAutoscalerCustomValidator) ValidateUpdate(ctx context.Context, oldObj, newObj runtime.Object) (admission.Warnings, error) {
    aas, ok := newObj.(*AgenticAutoscaler)
    if !ok {
        return nil, fmt.Errorf("expected AgenticAutoscaler, got %T", newObj)
    }
    return nil, webhook.ValidateSpec(&aas.Spec)
}

func (v *AgenticAutoscalerCustomValidator) ValidateDelete(ctx context.Context, obj runtime.Object) (admission.Warnings, error) {
    return nil, nil
}
```

(The exact type name might be `AgenticAutoscalerCustomValidator` or similar — match what kubebuilder generated.)

- [ ] **Step 3: Verify it compiles**

Run: `go build ./...`
Expected: clean.

- [ ] **Step 4: Commit**

```bash
git add api/v1alpha1/agenticautoscaler_webhook.go
git commit -m "feat(webhook): route ValidateCreate/Update through internal/webhook.ValidateSpec"
```

---

### Task 10: Webhook envtest (Tier-2)

**Files:**
- Create: `internal/controller/webhook_test.go`
- Modify: `internal/controller/suite_test.go`

We extend Plan #1's envtest scaffold to enable the webhook server. envtest can register webhooks against the in-memory API server.

- [ ] **Step 1: Modify suite_test.go to enable the webhook server**

Open `internal/controller/suite_test.go`. Modify the `BeforeSuite` hook to populate `WebhookInstallOptions`:

```go
testEnv = &envtest.Environment{
    CRDDirectoryPaths:     []string{filepath.Join(root, "config", "crd", "bases")},
    ErrorIfCRDPathMissing: true,
    WebhookInstallOptions: envtest.WebhookInstallOptions{
        Paths: []string{filepath.Join(root, "config", "webhook")},
    },
}
```

After `cfg, err := testEnv.Start()` and the existing scheme registration, set up a webhook server and register the validator:

```go
import (
    // add:
    ctrl "sigs.k8s.io/controller-runtime"
    "sigs.k8s.io/controller-runtime/pkg/manager"
    "sigs.k8s.io/controller-runtime/pkg/webhook"
)

var k8sManager manager.Manager

// inside BeforeSuite, after scheme registration:
k8sManager, err = ctrl.NewManager(cfg, ctrl.Options{
    Scheme: scheme.Scheme,
    WebhookServer: webhook.NewServer(webhook.Options{
        Host:    testEnv.WebhookInstallOptions.LocalServingHost,
        Port:    testEnv.WebhookInstallOptions.LocalServingPort,
        CertDir: testEnv.WebhookInstallOptions.LocalServingCertDir,
    }),
    Metrics: server.Options{BindAddress: "0"},
})
Expect(err).NotTo(HaveOccurred())

Expect((&autoscalingv1alpha1.AgenticAutoscaler{}).SetupWebhookWithManager(k8sManager)).To(Succeed())

go func() {
    defer GinkgoRecover()
    Expect(k8sManager.Start(context.Background())).To(Succeed())
}()
```

You may need to add `"context"` and `sigs.k8s.io/controller-runtime/pkg/manager/server"` imports.

- [ ] **Step 2: Write the webhook spec**

Create `internal/controller/webhook_test.go`:

```go
package controller_test

import (
    "context"

    . "github.com/onsi/ginkgo/v2"
    . "github.com/onsi/gomega"

    metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

    autoscalingv1alpha1 "github.com/pratyush-ghosh/agentic-autoscaler/api/v1alpha1"
)

var _ = Describe("Validating admission webhook", func() {
    var ns string

    BeforeEach(func() {
        ns = "default"
    })

    It("admits a valid CR", func() {
        min := int32(2)
        max := int32(10)
        cr := &autoscalingv1alpha1.AgenticAutoscaler{
            ObjectMeta: metav1.ObjectMeta{Name: "valid", Namespace: ns},
            Spec: autoscalingv1alpha1.AgenticAutoscalerSpec{
                TargetRef:   autoscalingv1alpha1.CrossVersionObjectReference{APIVersion: "apps/v1", Kind: "Deployment", Name: "demo"},
                MinReplicas: &min,
                MaxReplicas: &max,
            },
        }
        Expect(k8sClient.Create(context.Background(), cr)).To(Succeed())
        DeferCleanup(func() { _ = k8sClient.Delete(context.Background(), cr) })
    })

    It("rejects minReplicas below 1", func() {
        bad := int32(0)
        cr := &autoscalingv1alpha1.AgenticAutoscaler{
            ObjectMeta: metav1.ObjectMeta{Name: "bad-min", Namespace: ns},
            Spec: autoscalingv1alpha1.AgenticAutoscalerSpec{
                TargetRef:   autoscalingv1alpha1.CrossVersionObjectReference{APIVersion: "apps/v1", Kind: "Deployment", Name: "demo"},
                MinReplicas: &bad,
            },
        }
        err := k8sClient.Create(context.Background(), cr)
        Expect(err).To(HaveOccurred())
        Expect(err.Error()).To(ContainSubstring("minReplicas"))
    })

    It("rejects unknown preferredForecaster", func() {
        bad := "xgboost"
        cr := &autoscalingv1alpha1.AgenticAutoscaler{
            ObjectMeta: metav1.ObjectMeta{Name: "bad-forecaster", Namespace: ns},
            Spec: autoscalingv1alpha1.AgenticAutoscalerSpec{
                TargetRef:           autoscalingv1alpha1.CrossVersionObjectReference{APIVersion: "apps/v1", Kind: "Deployment", Name: "demo"},
                PreferredForecaster: &bad,
            },
        }
        err := k8sClient.Create(context.Background(), cr)
        Expect(err).To(HaveOccurred())
        Expect(err.Error()).To(ContainSubstring("preferredForecaster"))
    })

    It("rejects updates that violate rules", func() {
        min := int32(2)
        max := int32(10)
        cr := &autoscalingv1alpha1.AgenticAutoscaler{
            ObjectMeta: metav1.ObjectMeta{Name: "update-test", Namespace: ns},
            Spec: autoscalingv1alpha1.AgenticAutoscalerSpec{
                TargetRef:   autoscalingv1alpha1.CrossVersionObjectReference{APIVersion: "apps/v1", Kind: "Deployment", Name: "demo"},
                MinReplicas: &min,
                MaxReplicas: &max,
            },
        }
        Expect(k8sClient.Create(context.Background(), cr)).To(Succeed())
        DeferCleanup(func() { _ = k8sClient.Delete(context.Background(), cr) })

        bad := int32(1) // maxReplicas now < minReplicas
        cr.Spec.MaxReplicas = &bad
        err := k8sClient.Update(context.Background(), cr)
        Expect(err).To(HaveOccurred())
        Expect(err.Error()).To(ContainSubstring("maxReplicas"))
    })
})
```

- [ ] **Step 3: Run the envtest suite**

```bash
go test ./internal/controller/... -v
```

Expected: 4 specs PASS. The first run downloads etcd / kube-apiserver via envtest, so it may take a minute.

- [ ] **Step 4: Commit**

```bash
git add internal/controller/
git commit -m "test(webhook): cover create/update validation via envtest"
```

---

### Task 11: Final smoke and milestone

**Files:** none

- [ ] **Step 1: Lint and full test**

```bash
go vet ./...
go test ./...
```

Expected: clean.

- [ ] **Step 2: kubectl apply smoke (against any cluster with cert-manager installed)**

Optional but recommended:

```bash
# in a kind cluster with cert-manager v1.16 already installed:
kubectl apply -k config/default
kubectl get validatingwebhookconfiguration | grep agenticautoscaler

# attempt to apply an invalid CR; expect rejection:
cat <<'YAML' | kubectl apply --dry-run=server -f - || true
apiVersion: autoscaling.agentic.io/v1alpha1
kind: AgenticAutoscaler
metadata:
  name: bad-cr
  namespace: default
spec:
  targetRef:
    apiVersion: apps/v1
    kind: Deployment
    name: demo
  minReplicas: 0
YAML
```

Expected: `kubectl apply` returns an error containing `minReplicas=0 must be >= 1`.

- [ ] **Step 3: Milestone commit**

```bash
git commit --allow-empty -m "milestone: Plan #2 (validating admission webhook) complete

- ValidateSpec covers every design §4 bound check; multi-error aggregation
- Optional fields (cooldowns, maxStepSize, preferredForecaster) only check when non-nil
- cert-manager Issuer + Certificate wired through kubebuilder kustomize
- envtest covers create + update against four representative rules
- kubectl apply rejects bad CRs at admission time
"
```

---

## Plan-specific Definition of Done

- [ ] `go test ./internal/webhook/... -v -cover -count=1` shows every rule test passing; coverage on `internal/webhook` ≥ 95%.
- [ ] `go test ./internal/controller/... -v -count=1` shows the four webhook specs passing.
- [ ] `go vet ./...` clean.
- [ ] `kubectl kustomize config/default` renders without error.
- [ ] On a kind cluster with cert-manager v1.16 installed, `kubectl apply -k config/default` succeeds and creates a `ValidatingWebhookConfiguration` for `agenticautoscalers.autoscaling.agentic.io`.
- [ ] `kubectl apply` of a CR with `minReplicas: 0` is rejected with an error mentioning `minReplicas`.
- [ ] Multi-error aggregation: a CR violating three rules at once produces an error message mentioning all three field names.

---

## Notes on what's intentionally deferred

- **Mutating / defaulting webhooks** — design §4 specifies only validation; defaults come from kubebuilder markers in the CRD schema (covered by Plan #1 T8).
- **Admission webhook for the target Deployment** — out of scope; webhook fires only on `AgenticAutoscaler` resources.
- **Webhook deployment manifest** — already covered by `config/default/`. Plan #10's smoke applies it as part of the bootstrap.
- **Cert-manager installation** — Plan #10 / Plan #11. This plan assumes cert-manager is already running in the cluster (or in envtest, which manages its own certs).

---

## Self-Review (Spec Coverage, Placeholders, Type Consistency)

**Spec coverage.** Every row of design §4's "Admission webhook" list is in the Spec Coverage Map and has at least one test (T3-T7). Multi-error aggregation is in T7's `TestValidateSpec_AggregatesMultipleProblems`.

**Placeholders.** None. Every test contains real assertions; every code block is complete.

**Type consistency.**

- `validCR()` helper in tests uses the same field names as `AgenticAutoscalerSpec` from Plan #1: `TargetRef`, `MinReplicas`, `MaxReplicas`, `RpsPerPodMin`, `RpsPerPodMax`, `MaxStepSize`, `ScaleUpCooldownSeconds`, `ScaleDownCooldownSeconds`, `PreferredForecaster`.
- `webhook.ValidateSpec` signature `(spec *autoscalingv1alpha1.AgenticAutoscalerSpec) error` is the same in T2 (declaration), T3-T7 (consumer tests), and T9 (CustomValidator wiring).
- Allowed `preferredForecaster` values (T7) match Plan #7's dispatch literal type and Plan #1's CRD enum marker: `"prophet"`, `"linear_extrap"`, `"auto"`.

---

## Execution handoff

Plan complete and saved to `docs/superpowers/plans/2026-05-24-plan-02-validating-admission-webhook.md`. Two execution options:

1. **Subagent-Driven (recommended)** — I dispatch a fresh subagent per task, review between tasks, fast iteration.
2. **Inline Execution** — Execute tasks in this session using `executing-plans`, batch execution with checkpoints for review.

Which approach?


