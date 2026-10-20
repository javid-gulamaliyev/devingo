package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"devingo/internal/codegen"
)

func TestGoNameInitialisms(t *testing.T) {
	tests := map[string]string{
		"org_id":            "OrgID",
		"api_key":           "APIKey",
		"target_devin_uuid": "TargetDevinUUID",
		"httpURL":           "HTTPURL",
		"cycle_acu_limit":   "CycleACULimit",
		"pull-request-id":   "PullRequestID",
		"ipV6Address":       "IPV6Address",
	}
	for input, want := range tests {
		if got := codegen.GoName(input); got != want {
			t.Errorf("GoName(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestGenerateModels(t *testing.T) {
	spec := []byte(`{
  "components": {"schemas": {
    "State": {"type":"string", "enum":["open", "closed"]},
    "Child": {"type":"object", "required":["id"], "properties":{"id":{"type":"string"}}},
    "Record": {"type":"object", "required":["id", "nullable_required", "nullable_child", "labels", "details"], "properties":{
      "id":{"type":"string"},
      "nullable_required":{"type":"string", "nullable":true},
      "nullable_child":{"$ref":"#/components/schemas/Child", "nullable":true},
      "nullable_optional":{"type":"boolean", "nullable":true},
      "zero":{"type":"integer"},
      "maybe_labels":{"type":"array", "items":{"type":"string"}},
      "labels":{"type":"array", "items":{"type":"string"}},
      "details":{"type":"object", "additionalProperties":{"type":"integer"}},
      "child":{"$ref":"#/components/schemas/Child"},
      "state":{"$ref":"#/components/schemas/State"},
      "value":{"anyOf":[{"type":"string"},{"type":"integer"}]},
      "maybe":{"anyOf":[{"type":"string"},{"type":"null"}]},
      "unconstrained":{},
      "opaque_ref":{"$ref":"#/components/schemas/Opaque"}
    }},
    "Tuple": {"type":"array", "prefixItems":[{"type":"integer"},{"type":"integer"}]},
    "Combined": {"allOf":[{"$ref":"#/components/schemas/Child"},{"type":"object", "required":["enabled"], "properties":{"enabled":{"type":"boolean"}}}]},
    "Opaque": {"description":"Opaque JSON payload."},
    "OpenMap": {"type":"object", "additionalProperties":true}
  }}
}`)
	got, err := generate(spec, "sample", "")
	if err != nil {
		t.Fatal(err)
	}
	wantFragments := []string{
		"type Child struct",
		"// OpenAPI schema Child.",
		"type State string",
		"// OpenAPI schema State.",
		"StateOpen State = \"open\"",
		"ID string `json:\"id\"`",
		"// JSON field \"id\".",
		"NullableRequired *string `json:\"nullable_required\"`",
		"NullableChild *Child `json:\"nullable_child\"`",
		"NullableOptional Optional[bool] `json:\"nullable_optional,omitempty\"`",
		"Zero *int64 `json:\"zero,omitempty\"`",
		"MaybeLabels *[]string `json:\"maybe_labels,omitempty\"`",
		"Maybe Optional[string] `json:\"maybe,omitempty\"`",
		"Unconstrained Optional[json.RawMessage] `json:\"unconstrained,omitempty\"`",
		"OpaqueRef Optional[Opaque] `json:\"opaque_ref,omitempty\"`",
		"Labels []string `json:\"labels\"`",
		"Details map[string]int64 `json:\"details\"`",
		"Child *Child `json:\"child,omitempty\"`",
		"type RecordValue struct",
		"type Combined struct",
		"type Tuple [2]int64",
		"type Opaque = json.RawMessage",
		"// Opaque JSON payload.",
		"type OpenMap map[string]json.RawMessage",
		"func NewRecordValueString(value string) (RecordValue, error)",
		"func (value RecordValue) AsInteger() (int64, bool)",
	}
	compactOutput := compact(string(got))
	for _, fragment := range wantFragments {
		if !strings.Contains(compactOutput, compact(fragment)) {
			t.Errorf("generated output missing %q", fragment)
		}
	}
	if strings.Index(string(got), "type Child struct") > strings.Index(string(got), "type Record struct") {
		t.Error("models are not emitted in sorted name order")
	}
	second, err := generate(spec, "sample", "")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, second) {
		t.Error("generation is not deterministic")
	}
}

func compact(value string) string {
	return strings.NewReplacer(" ", "", "\n", "", "\t", "", "\r", "").Replace(value)
}

func TestGeneratedRawJSONRoundTrip(t *testing.T) {
	spec := []byte(`{"components":{"schemas":{"Opaque":{},"OpenObject":{"type":"object"}}}}`)
	generated, err := generate(spec, "fixture", "")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	files := map[string]string{
		"go.mod":    "module generatedfixture\n\ngo 1.23\n",
		"models.go": string(generated),
		"models_test.go": `package fixture
import (
  "encoding/json"
  "testing"
)
func TestOpaqueRawJSON(t *testing.T) {
  input := []byte("{\"key\":\"value\"}")
  var value Opaque
  if err := json.Unmarshal(input, &value); err != nil { t.Fatal(err) }
  encoded, err := json.Marshal(value)
  if err != nil { t.Fatal(err) }
  if string(encoded) != string(input) { t.Fatalf("got %s, want %s", encoded, input) }
}
func TestOpenObject(t *testing.T) {
  input := []byte("{\"key\":\"value\"}")
  var value OpenObject
  if err := json.Unmarshal(input, &value); err != nil { t.Fatal(err) }
  encoded, err := json.Marshal(value)
  if err != nil { t.Fatal(err) }
  if string(encoded) != string(input) { t.Fatalf("got %s, want %s", encoded, input) }
}
`,
	}
	for name, contents := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(contents), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	cmd := exec.Command("go", "test", "./...")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOCACHE="+filepath.Join(dir, "gocache"))
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("generated model round-trip test failed: %v\n%s", err, output)
	}
}

func TestGenerateSupportImport(t *testing.T) {
	spec := []byte(`{"components":{"schemas":{"Thing":{"type":"object","properties":{"name":{"type":"string","nullable":true}}}}}}`)
	got, err := generate(spec, "legacy", "devingo")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(got, []byte(`devin "devingo"`)) || !bytes.Contains(got, []byte("devin.Optional[string]")) {
		t.Fatalf("support import not applied:\n%s", got)
	}
}

func TestGenerateRejectsInvalidReferencesAndNameCollisions(t *testing.T) {
	tests := map[string]string{
		"missing ref":      `{"components":{"schemas":{"Thing":{"$ref":"#/components/schemas/Missing"}}}}`,
		"unsupported ref":  `{"components":{"schemas":{"Thing":{"$ref":"other.json#/Thing"}}}}`,
		"name collision":   `{"components":{"schemas":{"thing-id":{"type":"string"},"thing_id":{"type":"string"}}}}`,
		"nested collision": `{"components":{"schemas":{"Parent":{"type":"object","properties":{"child":{"type":"object","properties":{"name":{"type":"string"}}}}},"ParentChild":{"type":"string"}}}}`,
	}
	for name, input := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := generate([]byte(input), "sample", ""); err == nil {
				t.Fatal("generate() succeeded, want error")
			}
		})
	}
}
