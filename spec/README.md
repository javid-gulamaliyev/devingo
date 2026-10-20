# API contracts and provenance

The client is generated from checked-in contracts. Generation needs Go only,
does not download schemas, and does not contact Devin.

## Sources

- [Devin API overview](https://docs.devin.ai/api-reference/overview) and
  [documentation index](https://docs.devin.ai/llms.txt), inspected October 20, 2026.
- `v3.openapi.json`: the published OpenAPI subset in
  [CognitionAI/terraform-provider-devin](https://github.com/CognitionAI/terraform-provider-devin/blob/1adc3294eb3b7202e888400d96c554630bc86673/internal/api/openapi.json).
  This is a provider-focused subset, not the complete Devin API specification.
  Its Apache 2.0 license is retained in `LICENSE.provider`; blob provenance is
  recorded in `provenance.json`.
- `v1.documentation.openapi.json`, `v2.documentation.openapi.json`,
  `v3.documentation.openapi.json`, `sessions.openapi.json`, and
  `missing.openapi.json`: locally assembled contracts from the official endpoint
  references. These files are not official OpenAPI releases. Operation source
  URLs are recorded alongside the contracts and in `docs/endpoints.json`.
- `combined/v1.json`, `combined/v2.json`, `combined/v3.json`: deterministic merged
  generator inputs. Do not edit these files directly.

The official index links complete OpenAPI downloads, but those downloads were
inaccessible in the implementation environment. The accessible provider schema
and endpoint reference pages were used instead.

## Fidelity and known limitations

Endpoint coverage and complete schema fidelity are different claims. The
inventory checks route coverage; it cannot verify undocumented fields or server
behavior. Where reference pages expose field types, those types are retained.
Some nested fields are visible only in examples: their structures are inferred
from those examples and marked `x-example-derived-fields`. Such inferred fields
are optional; requiredness cannot be established from an example.

Unexpanded, unavailable, or deliberately dynamic JSON structures remain raw JSON
or maps of raw JSON. `x-contract-gap` annotations identify known gaps. In
particular, the `/v3/self` reference exposes only the service-user branch of a
four-variant identity union. The other branches preserve their JSON, without
inventing typed fields. Union accessors attempt decoding; they do not validate a
discriminator or enforce an OpenAPI `oneOf`.

Unknown JSON fields on fully typed structs follow `encoding/json` behavior and
are ignored. Use `Client.Do` with `json.RawMessage` when exact preservation of an
entire response is needed. Unknown enum strings are accepted. Client-side
validation checks required identifiers and bodies; it does not duplicate
evolving server validation rules.

## Updating

1. Update a source contract and its source URL/provenance.
2. Update `docs/endpoints.json` for new or removed routes.
3. Run `go generate .`.
4. Run `go test -race ./...` and `go vet ./...`.
5. Review generated public API changes and any fixture round-trip failures.

The contract suite checks every operation's method, path escaping, query
serialization, body and response fixtures, error propagation, and presence in
the independent endpoint inventory. Fixtures validate the captured contract;
they are not a substitute for a future opt-in live integration test.
