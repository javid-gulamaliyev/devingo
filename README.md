# Devin Go client

A standard-library-only Go client for Devin's API. It includes the current v3 API and separately typed v1 and v2 packages. The generated services and models come from the checked-in OpenAPI contracts; see [spec/README.md](spec/README.md) for their sources and provenance.

The [API index](docs/API.md) lists 294 operations: 226 in v3 (including beta routes), 20 in v1, and 48 in v2. Some detailed schemas were unavailable: those parts preserve raw JSON and have explicit [contract limitations](spec/README.md#fidelity-and-known-limitations).

The module path in this checkout is the local placeholder `devingo`. Import the root package as `devin`:

```go
import devin "devingo"
```

For another local module, add a replacement while developing:

```go
require devingo v0.0.0

replace devingo => ../devingo
```

## Create and retrieve a session

```go
client, err := devin.NewClient(os.Getenv("DEVIN_API_KEY"))
if err != nil {
	return err
}

ctx := context.Background()
sessions := client.Organization(os.Getenv("DEVIN_ORG_ID")).Sessions
created, response, err := sessions.Create(ctx, &devin.CreateSessionRequest{
	Prompt: "Summarize the open issues in this repository.",
}, nil)
if err != nil {
	return err
}
fmt.Printf("created %s (HTTP %d)\n", created.SessionID, response.StatusCode)

session, _, err := sessions.Get(ctx, created.SessionID)
if err != nil {
	return err
}
fmt.Println(session.Status)
```

Pass an organization ID to `Client.Organization` for organization-scoped methods. Use `Client.Enterprise()` for enterprise administration. The generated API groups methods by resource, with typed request and response models. This shape is familiar from clients such as go-github; nullable request values follow the same general need as Stripe-style optional parameters. The implementation, transport, and schema models here are independent.

The `v1` and `v2` subpackages expose their own version-specific clients and models. For example, `v1.NewClient(token, opts...)` provides the legacy `client.Sessions.Create` and `client.Sessions.Get` methods. They share the root HTTP transport. Reuse clients concurrently, and do not mutate request objects while calls are in progress.

## Optional and nullable fields

An optional, non-nullable field is generally a pointer. Use `devin.Ptr(false)` to send an explicit false value. An optional nullable field uses `Optional[T]`:

```go
request := devin.CreateSessionRequest{
	Prompt:    "Review this pull request.",
	Title:     devin.Null[string](), // send JSON null
	Tags:      devin.Value([]string{}), // send an explicit empty list
}
```

The zero `Optional[T]` omits the field. `Value(v)` includes a value, including `false`, `0`, and empty strings or lists. `Null[T]()` includes JSON `null`. Use `Ptr(v)` for optional fields represented as `*T` where null is not part of the schema. Unknown enum strings are retained by the named string types.

Open or undocumented JSON objects use `json.RawMessage` or `map[string]json.RawMessage`, so callers can inspect and preserve data without an `any`-typed schema.

## Errors, retries, and responses

Generated service methods return `(result, *devin.Response, error)`. The response includes status and request metadata; its body is already closed. Inspect API failures with `errors.As`:

```go
var apiErr *devin.APIError
if errors.As(err, &apiErr) {
	fmt.Println(apiErr.StatusCode, apiErr.RequestID, apiErr.Message)
}
```

Use `errors.Is` to check for `context.Canceled`, `context.DeadlineExceeded`, or `devin.ErrResponseTooLarge`. Error messages do not include API response fields, which may contain sensitive data. The structured `APIError` keeps the bounded response body available to the caller.

Retries are off by default. `WithRetryPolicy` enables bounded retries for bodyless `GET` and `HEAD` requests, including the initial attempt in `MaxAttempts`. Mutations, raw requests, and uploads are not retried. `Retry-After` is honored up to `MaxDelay`. Configure request deadlines with the context and transport timeout with `WithHTTPClient`.

The default HTTP timeout is 60 seconds. Successful response bodies are limited to 32 MiB; `WithMaxResponseBytes` changes this limit, and zero disables it. API error bodies remain limited to 64 KiB. `WithBaseURL` supports proxies and test transports; `WithUserAgent` customizes client identification. Redirects are disabled even on a supplied HTTP client.

For new query parameters, use an options struct's `Extra url.Values` field. Typed fields take precedence. For a new endpoint or an exact JSON response, use the shared transport directly:

```go
var raw json.RawMessage
response, err := client.Do(ctx, http.MethodGet, "/v3/self", nil, nil, &raw)
```

## Uploads and downloads

Generated upload methods stream the supplied reader as multipart data:

```go
file, err := os.Open("report.txt")
if err != nil {
	return err
}
defer file.Close()

uploaded, _, err := client.Organization(orgID).Attachments.Upload(ctx, &devin.Upload{
	Reader:   file,
	Filename: "report.txt",
})
```

The client does not buffer the file or retry an upload. The caller owns and closes the reader. A reader that blocks must be made cancelable by its owner.

`client.Organization(orgID).Attachments.Download` returns a signed HTTPS URL. `RedirectURL` validates the redirect and returns the URL without following it. Fetch the signed URL with a separate unauthenticated HTTP client before it expires; the Devin bearer token is not needed for that request.

## Pagination

The generic `Iterate` helper takes an adapter that maps an endpoint's response and options to `Page[T]`. It fetches lazily, follows cursors, respects cancellation and early breaks, and reports missing or repeated cursors. A compiling adapter using the session response types is in [example_test.go](example_test.go).

## Development

The project targets Go 1.23 and has no external Go dependencies. See [CONTRIBUTING.md](CONTRIBUTING.md) for testing and deterministic generation instructions.
