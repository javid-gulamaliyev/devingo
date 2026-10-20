package devin

import (
	"bytes"
	"net/http"
	"testing"
	"time"
)

func TestDecodeAPIErrorFormats(t *testing.T) {
	for _, tc := range []struct {
		name, body, message, detail string
		details                     int
	}{
		{"message", `{"message":"bad","detail":"problem"}`, "bad", "problem", 0},
		{"legacy error", `{"error":"failed"}`, "failed", "", 0},
		{"detail array", `{"detail":["one",{"msg":"two"}]}`, "", "", 2},
		{"plain text", `not JSON`, "", "", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := decodeAPIError(http.StatusBadRequest, http.MethodGet, "/v1/x", "request-id", []byte(tc.body), false)
			if e.Message != tc.message || e.Detail != tc.detail || len(e.Details) != tc.details || !bytes.Equal(e.Body, []byte(tc.body)) || e.RequestID != "request-id" {
				t.Fatalf("error fields: %+v", e)
			}
			if e.Error() != "devin: GET /v1/x: HTTP 400 Bad Request" {
				t.Fatalf("unsafe or unexpected error string: %q", e.Error())
			}
		})
	}
}

func TestParseRetryAfter(t *testing.T) {
	if got := parseRetryAfter("7", time.Now()); got != 7*time.Second {
		t.Fatal(got)
	}
	now := time.Now().UTC().Truncate(time.Second)
	if got := parseRetryAfter(now.Add(10*time.Second).Format(http.TimeFormat), now); got != 10*time.Second {
		t.Fatal(got)
	}
}

func TestProblemDetailError(t *testing.T) {
	e := decodeAPIError(422, "POST", "/v3/example", "req-1", []byte(`{
		"type":"about:blank","title":"Validation failed","detail":"Invalid input",
		"error_code":"validation_error","errors":[{"field":"prompt","message":"required"}]
	}`), false)
	if e.Code != "validation_error" || e.Title != "Validation failed" || e.Type != "about:blank" || len(e.Details) != 1 {
		t.Fatalf("problem detail = %+v", e)
	}
}
