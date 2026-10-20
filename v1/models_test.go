package v1

import (
	"encoding/json"
	"testing"
)

func TestSessionSummaryStructuredOutputPreservesNull(t *testing.T) {
	var summary SessionSummary
	if err := json.Unmarshal([]byte(`{"structured_output":null}`), &summary); err != nil {
		t.Fatal(err)
	}
	if !summary.StructuredOutput.IsNull() {
		t.Fatal("explicit null was not retained")
	}
	encoded, err := json.Marshal(summary)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &fields); err != nil {
		t.Fatal(err)
	}
	if string(fields["structured_output"]) != "null" {
		t.Fatalf("structured_output changed: %s", fields["structured_output"])
	}
	var unset SessionSummary
	encoded, err = json.Marshal(unset)
	if err != nil {
		t.Fatal(err)
	}
	fields = nil
	if err := json.Unmarshal(encoded, &fields); err != nil {
		t.Fatal(err)
	}
	if _, exists := fields["structured_output"]; exists {
		t.Fatal("unset structured_output was emitted")
	}
}
