package devin

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestGeneratedSessionRequestOptionalStates(t *testing.T) {
	request := CreateSessionRequest{
		Prompt:                 "run this",
		BypassApproval:         Value(false),
		MaxACULimit:            Value(int64(0)),
		Tags:                   Value([]string{}),
		AttachmentURLs:         Null[[]string](),
		DevinMode:              Value(CreateSessionRequestDevinMode("future-mode")),
		StructuredOutputSchema: Value(map[string]json.RawMessage{"deep": json.RawMessage(`{"nested":[1,true]}`)}),
	}
	encoded, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &object); err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]string{
		"prompt":                   `"run this"`,
		"bypass_approval":          `false`,
		"max_acu_limit":            `0`,
		"tags":                     `[]`,
		"attachment_urls":          `null`,
		"devin_mode":               `"future-mode"`,
		"structured_output_schema": `{"deep":{"nested":[1,true]}}`,
	} {
		var gotValue, wantValue any
		if err := json.Unmarshal(object[key], &gotValue); err != nil {
			t.Fatalf("%s: %v", key, err)
		}
		if err := json.Unmarshal([]byte(want), &wantValue); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(gotValue, wantValue) {
			t.Errorf("%s = %s, want %s", key, object[key], want)
		}
	}
	if _, exists := object["title"]; exists {
		t.Fatal("unset optional title was emitted")
	}
	var decoded CreateSessionRequest
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	if value, ok := decoded.BypassApproval.Get(); !ok || value {
		t.Fatal("false optional value lost")
	}
	if !decoded.AttachmentURLs.IsNull() || !decoded.Tags.IsSet() {
		t.Fatal("null or empty slice optional state lost")
	}
	if value, ok := decoded.Tags.Get(); !ok || value == nil || len(value) != 0 {
		t.Fatalf("empty slice changed: %#v, %t", value, ok)
	}
	if mode, ok := decoded.DevinMode.Get(); !ok || string(mode) != "future-mode" {
		t.Fatalf("unknown enum value changed: %q, %t", mode, ok)
	}
}

func TestGeneratedAutomationUnionPreservesNestedJSON(t *testing.T) {
	input := []byte(`{"kind":"future_action","payload":{"steps":[{"id":1},null,true],"zero":0}}`)
	var action AutomationCreateRequestActionsItem
	if err := json.Unmarshal(input, &action); err != nil {
		t.Fatal(err)
	}
	output, err := json.Marshal(action)
	if err != nil {
		t.Fatal(err)
	}
	var got, want any
	if err := json.Unmarshal(output, &got); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(input, &want); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("union changed JSON: %s", output)
	}
}

func TestGeneratedSessionInsightsNestedFields(t *testing.T) {
	input := []byte(`{"session_id":"devin-1","num_devin_messages":4,"num_user_messages":2,"session_size":"m","analysis":{"action_items":[{"topic":{"id":7}}]},"structured_output":{"answer":{"nested":[1,2]}}}`)
	var model SessionInsightsResponse
	if err := json.Unmarshal(input, &model); err != nil {
		t.Fatal(err)
	}
	analysis, ok := model.Analysis.Get()
	if !ok || analysis.ActionItems == nil || len(*analysis.ActionItems) != 1 || model.NumDevinMessages != 4 || model.SessionID != "devin-1" {
		t.Fatalf("flattened or nested fields lost: %+v", model)
	}
	output, err := json.Marshal(model)
	if err != nil {
		t.Fatal(err)
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(output, &object); err != nil {
		t.Fatal(err)
	}
	var nested map[string]json.RawMessage
	if err := json.Unmarshal(object["structured_output"], &nested); err != nil {
		t.Fatal(err)
	}
	if string(nested["answer"]) != `{"nested":[1,2]}` {
		t.Fatalf("structured output changed: %s", nested["answer"])
	}
}
