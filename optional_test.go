package devin

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"
)

func TestOptionalOmitEmptyAndMarshalStates(t *testing.T) {
	tests := []struct {
		name  string
		value Optional[any]
		want  string
	}{
		{name: "unset nil", value: nil, want: `{}`},
		{name: "unset empty", value: Optional[any]{}, want: `{}`},
		{name: "explicit null", value: Null[any](), want: `{"field":null}`},
		{name: "false", value: Value[any](false), want: `{"field":false}`},
		{name: "zero", value: Value[any](0), want: `{"field":0}`},
		{name: "empty string", value: Value[any](""), want: `{"field":""}`},
		{name: "empty list", value: Value[any]([]int{}), want: `{"field":[]}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := json.Marshal(struct {
				Field Optional[any] `json:"field,omitempty"`
			}{Field: tt.value})
			if err != nil {
				t.Fatalf("Marshal() error = %v", err)
			}
			if string(got) != tt.want {
				t.Fatalf("Marshal() = %s, want %s", got, tt.want)
			}
		})
	}
}

func TestOptionalUnmarshalStatesAndRequestRoundTrip(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantSet bool
		wantNil bool
		want    any
	}{
		{name: "null", input: `{"field":null}`, wantSet: true, wantNil: true},
		{name: "false", input: `{"field":false}`, wantSet: true, want: false},
		{name: "zero", input: `{"field":0}`, wantSet: true, want: float64(0)},
		{name: "empty string", input: `{"field":""}`, wantSet: true, want: ""},
		{name: "empty list", input: `{"field":[]}`, wantSet: true, want: []any{}},
		{name: "omitted", input: `{}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var decoded struct {
				Field Optional[any] `json:"field,omitempty"`
			}
			if err := json.Unmarshal([]byte(tt.input), &decoded); err != nil {
				t.Fatalf("Unmarshal() error = %v", err)
			}
			if decoded.Field.IsSet() != tt.wantSet || decoded.Field.IsNull() != tt.wantNil {
				t.Fatalf("state = (set %v, null %v), want (set %v, null %v)", decoded.Field.IsSet(), decoded.Field.IsNull(), tt.wantSet, tt.wantNil)
			}
			value, ok := decoded.Field.Get()
			if tt.wantNil {
				if ok {
					t.Fatalf("Get() = (%v, true), want null", value)
				}
			} else if tt.wantSet {
				if !ok || !reflect.DeepEqual(value, tt.want) {
					t.Fatalf("Get() = (%#v, %v), want (%#v, true)", value, ok, tt.want)
				}
			}

			encoded, err := json.Marshal(decoded)
			if err != nil {
				t.Fatalf("Marshal() error = %v", err)
			}
			var roundTrip struct {
				Field Optional[any] `json:"field,omitempty"`
			}
			if err := json.Unmarshal(encoded, &roundTrip); err != nil {
				t.Fatalf("round-trip Unmarshal() error = %v", err)
			}
			if roundTrip.Field.IsSet() != tt.wantSet || roundTrip.Field.IsNull() != tt.wantNil {
				t.Fatalf("round-trip state = (set %v, null %v), want (set %v, null %v)", roundTrip.Field.IsSet(), roundTrip.Field.IsNull(), tt.wantSet, tt.wantNil)
			}
		})
	}
}

func TestOptionalMalformedJSON(t *testing.T) {
	for _, input := range []string{`tru`, `1 2`, `null false`, `{"x":1}`} {
		var value Optional[bool]
		if err := json.Unmarshal([]byte(input), &value); err == nil {
			t.Errorf("Unmarshal(%q) succeeded, want error", input)
		}
	}
}

func TestOptionalNilReceiverUnmarshal(t *testing.T) {
	var value *Optional[int]
	if err := value.UnmarshalJSON([]byte("null")); err == nil {
		t.Fatal("UnmarshalJSON() succeeded on nil receiver, want error")
	}
}

func TestOptionalInvalidMapState(t *testing.T) {
	value := Optional[int]{false: 0, true: 1}
	if _, err := json.Marshal(value); !errors.Is(err, errInvalidOptional) {
		t.Fatalf("Marshal() error = %v, want invalid-state error", err)
	}
}
