# Contributing

The client targets Go 1.23 and uses the standard library only. The current module path is the local placeholder `devingo`; keep examples and local development imports aligned with it unless the module path is intentionally changed.

Generated files are produced from the checked-in contracts under `spec/`. Edit the generators or source contracts rather than generated `.gen.go` files. Generation does not require network access:

```sh
go generate .
```

Before opening a change, run the same checks as CI:

```sh
go test ./...
go vet ./...
go test -race ./...
go generate .
git diff --exit-code
```

The final diff check confirms that generation is deterministic and that generated output was updated when its inputs changed. Add regression tests for behavior changes, especially request construction, response decoding, pagination, retries, and streaming I/O. Do not include API tokens or signed download URLs in tests or logs.
