package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// withFixture runs run with a minimal contract tree rooted in a temporary
// directory. The working directory is process-global, so these tests must not
// run in parallel.
func withFixture(t *testing.T, files map[string]any, wantErr string) map[string]any {
	t.Helper()
	root := t.TempDir()
	for path, value := range files {
		data, err := json.Marshal(value)
		if err != nil {
			t.Fatalf("marshal fixture %s: %v", path, err)
		}
		fullPath := filepath.Join(root, path)
		if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
			t.Fatalf("create fixture directory: %v", err)
		}
		if err := os.WriteFile(fullPath, data, 0o644); err != nil {
			t.Fatalf("write fixture %s: %v", path, err)
		}
	}
	oldWD, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(oldWD); err != nil {
			t.Errorf("restore working directory: %v", err)
		}
	})

	err = run()
	if wantErr != "" {
		if err == nil || !strings.Contains(err.Error(), wantErr) {
			t.Fatalf("run() error = %v, want substring %q", err, wantErr)
		}
		return nil
	}
	if err != nil {
		t.Fatalf("run() error = %v", err)
	}
	var merged map[string]any
	data, err := os.ReadFile("spec/combined/v1.json")
	if err != nil {
		t.Fatalf("read merged v1 spec: %v", err)
	}
	if err := json.Unmarshal(data, &merged); err != nil {
		t.Fatalf("decode merged v1 spec: %v", err)
	}
	return merged
}

func emptyDocument(paths, schemas map[string]any) map[string]any {
	if paths == nil {
		paths = map[string]any{}
	}
	if schemas == nil {
		schemas = map[string]any{}
	}
	return map[string]any{
		"paths":      paths,
		"components": map[string]any{"schemas": schemas},
	}
}

func baseFiles(v1Doc, v2Doc, v3Doc, v3Base, sessions, missing map[string]any) map[string]any {
	return map[string]any{
		"docs/endpoints.json":                []any{},
		"spec/v1.documentation.openapi.json": v1Doc,
		"spec/v2.documentation.openapi.json": v2Doc,
		"spec/v3.documentation.openapi.json": v3Doc,
		"spec/v3.openapi.json":               v3Base,
		"spec/sessions.openapi.json":         sessions,
		"spec/missing.openapi.json":          missing,
	}
}

func TestRunMergesReachableSchemasAndKeepsProblemDetailProviderDefinition(t *testing.T) {
	ref := func(name string) map[string]any {
		return map[string]any{"$ref": "#/components/schemas/" + name}
	}
	operation := map[string]any{"get": map[string]any{"responses": map[string]any{
		"200": map[string]any{"content": map[string]any{"application/json": map[string]any{"schema": ref("Root")}}},
	}}}
	providerProblem := map[string]any{"type": "object", "properties": map[string]any{"provider": map[string]any{"type": "string"}}}
	docsProblem := map[string]any{"type": "object", "properties": map[string]any{"docs": map[string]any{"type": "string"}}}
	crossVersionOperation := map[string]any{"get": map[string]any{"responses": map[string]any{
		"200": map[string]any{"content": map[string]any{"application/json": map[string]any{"schema": ref("V2Only")}}},
	}}}
	v1 := emptyDocument(map[string]any{"/v1/items": operation, "/v2/cross-version": crossVersionOperation}, map[string]any{
		"Root":   map[string]any{"type": "object", "properties": map[string]any{"child": ref("Child")}},
		"Child":  map[string]any{"type": "string"},
		"Orphan": map[string]any{"type": "boolean"},
		"V2Only": map[string]any{"type": "integer"},
	})
	v3Provider := emptyDocument(map[string]any{"/v3/items": map[string]any{"get": map[string]any{"responses": map[string]any{
		"200": map[string]any{"content": map[string]any{"application/json": map[string]any{"schema": ref("ProblemDetail")}}},
	}}}}, map[string]any{"ProblemDetail": providerProblem})
	v3Docs := emptyDocument(nil, map[string]any{"ProblemDetail": docsProblem})
	files := baseFiles(v1, emptyDocument(nil, nil), v3Docs, v3Provider, emptyDocument(nil, nil), emptyDocument(nil, nil))
	mergedV1 := withFixture(t, files, "")

	v1Schemas := mergedV1["components"].(map[string]any)["schemas"].(map[string]any)
	if len(v1Schemas) != 2 || v1Schemas["Root"] == nil || v1Schemas["Child"] == nil {
		t.Fatalf("v1 schemas = %#v, want only reachable Root and Child", v1Schemas)
	}
	v3Data, err := os.ReadFile("spec/combined/v3.json")
	if err != nil {
		t.Fatal(err)
	}
	var mergedV3 map[string]any
	if err := json.Unmarshal(v3Data, &mergedV3); err != nil {
		t.Fatal(err)
	}
	problem := mergedV3["components"].(map[string]any)["schemas"].(map[string]any)["ProblemDetail"]
	problemJSON, _ := json.Marshal(problem)
	if !strings.Contains(string(problemJSON), `"provider"`) || strings.Contains(string(problemJSON), `"docs"`) {
		t.Fatalf("merged ProblemDetail = %s, want provider definition", problemJSON)
	}
}

func TestRunRejectsDuplicateOperations(t *testing.T) {
	path := "/v1/items"
	op := map[string]any{"get": map[string]any{"responses": map[string]any{}}}
	files := baseFiles(emptyDocument(map[string]any{path: op}, nil), emptyDocument(nil, nil), emptyDocument(nil, nil), emptyDocument(nil, nil), emptyDocument(nil, nil), emptyDocument(map[string]any{path: op}, nil))
	withFixture(t, files, "duplicate operation get /v1/items")
}

func TestRunRejectsConflictingSchemaCollision(t *testing.T) {
	files := baseFiles(
		emptyDocument(nil, map[string]any{"Collision": map[string]any{"type": "string"}}),
		emptyDocument(nil, nil), emptyDocument(nil, nil), emptyDocument(nil, nil), emptyDocument(nil, nil),
		emptyDocument(nil, map[string]any{"Collision": map[string]any{"type": "boolean"}}),
	)
	withFixture(t, files, "conflicting schema Collision")
}

func TestRunRejectsUnresolvedReferences(t *testing.T) {
	op := map[string]any{"get": map[string]any{"responses": map[string]any{
		"200": map[string]any{"content": map[string]any{"application/json": map[string]any{"schema": map[string]any{"$ref": "#/components/schemas/Missing"}}}},
	}}}
	files := baseFiles(emptyDocument(map[string]any{"/v1/items": op}, nil), emptyDocument(nil, nil), emptyDocument(nil, nil), emptyDocument(nil, nil), emptyDocument(nil, nil), emptyDocument(nil, nil))
	withFixture(t, files, "unresolved model Missing")
}
