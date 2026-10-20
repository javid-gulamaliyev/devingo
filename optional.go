package devin

import (
	"bytes"
	"encoding/json"
	"errors"
)

var errInvalidOptional = errors.New("optional value has both null and value states")

// Optional represents an optional, nullable value. Its nil or empty map is
// unset, a false key represents explicit JSON null, and a true key represents
// a value. Use Value and Null to construct values; callers should not mutate
// the map directly.
type Optional[T any] map[bool]T

// Value constructs a set Optional containing v, including when v is a zero
// value.
func Value[T any](v T) Optional[T] { return Optional[T]{true: v} }

// Null constructs an Optional that is explicitly set to JSON null.
func Null[T any]() Optional[T] { return Optional[T]{false: *new(T)} }

// IsSet reports whether this Optional represents an explicit value or null.
func (o Optional[T]) IsSet() bool { return len(o) != 0 }

// IsNull reports whether this Optional represents explicit JSON null.
func (o Optional[T]) IsNull() bool {
	_, isNull := o[false]
	return isNull
}

// Get returns the value and true when the Optional contains a value. It
// returns the zero value and false for unset and explicit null states.
func (o Optional[T]) Get() (T, bool) {
	value, ok := o[true]
	return value, ok
}

// MarshalJSON encodes an unset or explicitly null Optional as JSON null, or
// encodes its contained value. An Optional with both map keys is invalid.
func (o Optional[T]) MarshalJSON() ([]byte, error) {
	if len(o) > 1 {
		return nil, errInvalidOptional
	}
	if value, ok := o[true]; ok {
		return json.Marshal(value)
	}
	return []byte("null"), nil
}

// UnmarshalJSON decodes JSON null as explicit null and any other JSON value
// as a set value. The receiver must be non-nil.
func (o *Optional[T]) UnmarshalJSON(data []byte) error {
	if o == nil {
		return errors.New("cannot unmarshal Optional into a nil receiver")
	}
	trimmed := bytes.TrimSpace(data)
	if bytes.Equal(trimmed, []byte("null")) {
		*o = Null[T]()
		return nil
	}
	var value T
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	*o = Value(value)
	return nil
}
