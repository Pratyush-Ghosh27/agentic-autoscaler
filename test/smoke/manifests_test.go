/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

// Package smoke contains tier-3 manifest validation tests:
//
//  1. Every YAML under deploy/manifests/ parses cleanly through
//     k8s.io/apimachinery's universal decoder.
//  2. target-agentic.yaml and target-hpa.yaml have byte-identical
//     .spec.template.spec — the only differences allowed are in
//     metadata.name, metadata.labels, selectors, and the pod's labels.
//  3. The Grafana dashboard JSON parses, has the expected uid, and
//     contains exactly 7 panels.
//  4. Cross-manifest invariants: HPA.scaleTargetRef.name == "app-hpa";
//     AgenticAutoscaler.spec.targetRef.name == "app-agentic".
//
// These checks run as part of `go test ./...` so a typo in a manifest
// fails CI before kind ever spins up.
package smoke_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"sigs.k8s.io/yaml"
)

// repoRoot resolves the workspace root by walking up from the test file
// until it finds go.work — robust to where 'go test' was invoked.
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	require.NoError(t, err)
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.work")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.work not found while walking up from test working directory")
		}
		dir = parent
	}
}

// decodeAllYAMLDocs splits a multi-document YAML stream into per-document
// `map[string]interface{}` values via sigs.k8s.io/yaml round-trip.
func decodeAllYAMLDocs(t *testing.T, data []byte) []map[string]any {
	t.Helper()
	docs := splitYAMLDocs(data)
	out := make([]map[string]any, 0, len(docs))
	for _, raw := range docs {
		if len(strings.TrimSpace(string(raw))) == 0 {
			continue
		}
		var m map[string]any
		require.NoError(t, yaml.Unmarshal(raw, &m), "unmarshal yaml doc:\n%s", string(raw))
		if len(m) == 0 {
			continue
		}
		out = append(out, m)
	}
	return out
}

// splitYAMLDocs is a tiny `---` splitter sufficient for our deploy/ corpus.
// It does not handle the edge case of `---` appearing inside a literal
// block scalar, but none of our manifests use one.
func splitYAMLDocs(data []byte) [][]byte {
	parts := strings.Split(string(data), "\n---")
	out := make([][]byte, 0, len(parts))
	for _, p := range parts {
		out = append(out, []byte(strings.TrimPrefix(p, "---\n")))
	}
	return out
}

func TestAllManifests_ParseAsYAMLAndHaveKindAndAPIVersion(t *testing.T) {
	root := repoRoot(t)
	manifestsDir := filepath.Join(root, "deploy", "manifests")

	entries, err := os.ReadDir(manifestsDir)
	require.NoError(t, err)
	require.NotEmpty(t, entries)

	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".yaml") {
			continue
		}
		t.Run(e.Name(), func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join(manifestsDir, e.Name()))
			require.NoError(t, err)

			docs := decodeAllYAMLDocs(t, data)
			require.NotEmpty(t, docs, "%s parsed to zero docs", e.Name())

			for i, d := range docs {
				assert.NotEmpty(t, d["apiVersion"], "doc %d missing apiVersion in %s", i, e.Name())
				assert.NotEmpty(t, d["kind"], "doc %d missing kind in %s", i, e.Name())

				meta, ok := d["metadata"].(map[string]any)
				require.True(t, ok, "doc %d metadata not a map in %s", i, e.Name())
				assert.NotEmpty(t, meta["name"], "doc %d missing metadata.name in %s", i, e.Name())
			}
		})
	}
}

func TestTargetDeployments_PodTemplateSpecsAreIdentical(t *testing.T) {
	root := repoRoot(t)
	agentic := loadFirstDeployment(t, filepath.Join(root, "deploy", "manifests", "target-agentic.yaml"))
	hpa := loadFirstDeployment(t, filepath.Join(root, "deploy", "manifests", "target-hpa.yaml"))

	agenticTpl := getPath(t, agentic, "spec", "template", "spec")
	hpaTpl := getPath(t, hpa, "spec", "template", "spec")

	if !reflect.DeepEqual(agenticTpl, hpaTpl) {
		t.Fatalf("PodTemplate .spec.template.spec mismatch — the comparison must be apples-to-apples.\nagentic: %#v\nhpa:     %#v", agenticTpl, hpaTpl)
	}
}

func loadFirstDeployment(t *testing.T, path string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	for _, d := range decodeAllYAMLDocs(t, data) {
		if d["kind"] == "Deployment" {
			return d
		}
	}
	t.Fatalf("no Deployment found in %s", path)
	return nil
}

func getPath(t *testing.T, obj map[string]any, keys ...string) any {
	t.Helper()
	var cur any = obj
	for _, k := range keys {
		m, ok := cur.(map[string]any)
		require.True(t, ok, "path %v is not a map at %q (got %T)", keys, k, cur)
		cur = m[k]
	}
	return cur
}

func TestHPA_TargetsAppHpa(t *testing.T) {
	root := repoRoot(t)
	data, err := os.ReadFile(filepath.Join(root, "deploy", "manifests", "hpa.yaml"))
	require.NoError(t, err)

	docs := decodeAllYAMLDocs(t, data)
	require.Len(t, docs, 1)
	hpa := docs[0]

	assert.Equal(t, "HorizontalPodAutoscaler", hpa["kind"])
	ref := getPath(t, hpa, "spec", "scaleTargetRef")
	refMap := ref.(map[string]any)
	assert.Equal(t, "Deployment", refMap["kind"])
	assert.Equal(t, "app-hpa", refMap["name"])
}

func TestAgenticAutoscalerSample_TargetsAppAgentic(t *testing.T) {
	root := repoRoot(t)
	data, err := os.ReadFile(filepath.Join(root, "deploy", "manifests", "agenticautoscaler-sample.yaml"))
	require.NoError(t, err)

	docs := decodeAllYAMLDocs(t, data)
	require.Len(t, docs, 1)
	cr := docs[0]

	assert.Equal(t, "AgenticAutoscaler", cr["kind"])
	ref := getPath(t, cr, "spec", "targetRef")
	refMap := ref.(map[string]any)
	assert.Equal(t, "Deployment", refMap["kind"])
	assert.Equal(t, "app-agentic", refMap["name"])
}

func TestGrafanaDashboard_ValidJSONWithSevenPanels(t *testing.T) {
	root := repoRoot(t)
	data, err := os.ReadFile(filepath.Join(root, "deploy", "grafana", "agentic-dashboard.json"))
	require.NoError(t, err)

	var dash map[string]any
	require.NoError(t, json.Unmarshal(data, &dash))

	assert.Equal(t, "agentic-autoscaler", dash["uid"])
	assert.Equal(t, "Agentic Autoscaler", dash["title"])

	panels, ok := dash["panels"].([]any)
	require.True(t, ok, "panels must be an array")
	assert.Len(t, panels, 7, "dashboard must have exactly 7 panels — see strategy doc §11 R9")

	for i, p := range panels {
		pm := p.(map[string]any)
		assert.NotEmpty(t, pm["title"], "panel %d missing title", i)
		assert.NotEmpty(t, pm["type"], "panel %d missing type", i)
	}
}

func TestGrafanaDashboardConfigMap_HasSidecarLabel(t *testing.T) {
	root := repoRoot(t)
	data, err := os.ReadFile(filepath.Join(root, "deploy", "grafana", "dashboard-configmap.yaml"))
	require.NoError(t, err)

	docs := decodeAllYAMLDocs(t, data)
	require.Len(t, docs, 1)
	cm := docs[0]

	meta := getPath(t, cm, "metadata").(map[string]any)
	labels, ok := meta["labels"].(map[string]any)
	require.True(t, ok, "configmap must have labels")
	assert.Equal(t, "1", labels["grafana_dashboard"],
		`label "grafana_dashboard: 1" is required by the kube-prometheus-stack sidecar`)
	assert.Equal(t, "monitoring", meta["namespace"],
		"sidecar watches the monitoring namespace per prometheus-values.yaml")
}

func TestGrafanaKustomization_GeneratesDashboardConfigMap(t *testing.T) {
	root := repoRoot(t)
	data, err := os.ReadFile(filepath.Join(root, "deploy", "grafana", "kustomization.yaml"))
	require.NoError(t, err)

	docs := decodeAllYAMLDocs(t, data)
	require.Len(t, docs, 1)
	k := docs[0]

	assert.Equal(t, "Kustomization", k["kind"])
	assert.Equal(t, "monitoring", k["namespace"])

	gen, ok := k["configMapGenerator"].([]any)
	require.True(t, ok)
	require.Len(t, gen, 1)

	first := gen[0].(map[string]any)
	assert.Equal(t, "agentic-dashboard", first["name"])
	files := first["files"].([]any)
	require.NotEmpty(t, files)
	assert.Contains(t, files[0].(string), "agentic-dashboard.json")
}

func TestKindCluster_ThreeNodes(t *testing.T) {
	root := repoRoot(t)
	data, err := os.ReadFile(filepath.Join(root, "deploy", "kind", "cluster.yaml"))
	require.NoError(t, err)

	docs := decodeAllYAMLDocs(t, data)
	require.Len(t, docs, 1)
	cluster := docs[0]

	assert.Equal(t, "Cluster", cluster["kind"])
	nodes, ok := cluster["nodes"].([]any)
	require.True(t, ok)
	assert.Len(t, nodes, 3, "kind cluster must have exactly 3 nodes per design")
}

func TestPrometheusValues_HasGrafanaSidecarEnabled(t *testing.T) {
	root := repoRoot(t)
	data, err := os.ReadFile(filepath.Join(root, "deploy", "helm", "prometheus-values.yaml"))
	require.NoError(t, err)

	docs := decodeAllYAMLDocs(t, data)
	require.Len(t, docs, 1)
	v := docs[0]

	sidecar := getPath(t, v, "grafana", "sidecar", "dashboards")
	sm := sidecar.(map[string]any)
	assert.Equal(t, true, sm["enabled"])
	assert.Equal(t, "grafana_dashboard", sm["label"])
}
