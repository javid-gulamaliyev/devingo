package devin

// Ptr returns a pointer to value, including zero values, for optional fields.
func Ptr[T any](value T) *T { return &value }
