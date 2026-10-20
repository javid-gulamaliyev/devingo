package devin

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
)

// ErrResponseTooLarge indicates that a response exceeded the configured limit.
var ErrResponseTooLarge = errors.New("devin: response exceeds size limit")

// APIError is a non-2xx response from the Devin API. Body is capped at 64 KiB.
// Error deliberately excludes response fields, which may contain secrets.
type APIError struct {
	StatusCode int
	Method     string
	Path       string
	RequestID  string
	Message    string
	Code       string
	Type       string
	Title      string
	Detail     string
	Details    []any
	Body       []byte
	Truncated  bool
}

func (e *APIError) Error() string {
	if e == nil {
		return "devin: API error"
	}
	return fmt.Sprintf("devin: %s %s: HTTP %d %s", e.Method, e.Path, e.StatusCode, http.StatusText(e.StatusCode))
}

func decodeAPIError(status int, method, path, requestID string, body []byte, truncated bool) *APIError {
	e := &APIError{
		StatusCode: status,
		Method:     method,
		Path:       path,
		RequestID:  requestID,
		Body:       body,
		Truncated:  truncated,
	}
	var object map[string]json.RawMessage
	if json.Unmarshal(body, &object) != nil {
		return e
	}
	readString := func(key string) string {
		var value string
		_ = json.Unmarshal(object[key], &value)
		return value
	}
	e.Message = readString("message")
	if e.Message == "" {
		e.Message = readString("error")
	}
	e.Code = readString("code")
	if e.Code == "" {
		e.Code = readString("error_code")
	}
	e.Type = readString("type")
	e.Title = readString("title")
	if raw := object["detail"]; len(raw) != 0 {
		if json.Unmarshal(raw, &e.Detail) != nil {
			_ = json.Unmarshal(raw, &e.Details)
		}
	}
	if len(e.Details) == 0 {
		_ = json.Unmarshal(object["errors"], &e.Details)
	}
	return e
}
