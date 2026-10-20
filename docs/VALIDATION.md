# Validation

Verified locally with Go 1.27.2 on macOS arm64. CI is configured for Go 1.23
and the stable Go release; CI has not been run on a remote repository.

- All 294 inventoried operations have contract tests: 226 v3, 20 v1, 48 v2.
  Every operation exercises successful responses and API errors. The suite
  also checks path escaping, repeated query parameters, explicit zero values,
  multipart bodies, and JSON fixture preservation.
- `go test -race ./...` passes, including concurrent client reuse.
- `go vet ./...` passes.
- `gofmt -l .` reports no files.
- Repeated `go generate .` produces byte-identical generated outputs.
- Both fuzz targets pass short, two-worker fuzz runs: optional JSON
  round-tripping and escaped-path origin preservation.
- Handwritten client code has 87.6% statement coverage. Generated code and
  generator tools are excluded from this figure. Coverage was collected with
  `go test -race -coverpkg=./... -coverprofile=coverage.out ./...`; repeated
  profile blocks were merged before calculating the handwritten-code total.

The transport tests use in-memory HTTP round trippers, so they require no
network listener, credentials, or Devin credits. These checks do not establish
live API compatibility. See [schema provenance and limitations](../spec/README.md)
for the boundaries of the captured contracts.
