// Package devin provides a standard-library-only client for Devin's v3 APIs.
//
// Construct a Client with NewClient, select an organization with Organization,
// or use Enterprise for enterprise administration. Resource methods accept a
// context and return the decoded result, HTTP response metadata, and an error.
// A client may be reused concurrently; request objects must not be mutated
// while in use.
//
// Optional fields use pointers to preserve explicit zero values. Optional
// nullable fields use Optional: leave it unset to omit, use Value to supply a
// value, or Null to send JSON null. Unknown enum strings remain valid values.
// JSON schemas and fields without a documented structure preserve raw JSON.
//
// Automatic retries are disabled. WithRetryPolicy enables bounded retries only
// for bodyless GET and HEAD requests. Mutations and uploads are never retried.
// Use errors.As to inspect APIError and errors.Is for context cancellation.
//
// The v1 and v2 subpackages contain the separately typed legacy APIs.
package devin
