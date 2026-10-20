# Implementation plan

1. Inventory every endpoint in the official API documentation, including v3
   organization and enterprise APIs, beta routes, and legacy v1/v2.
2. Capture source contracts and provenance. Distinguish published OpenAPI from
   contracts reconstructed from documentation and examples.
3. Implement a small shared HTTP client using the standard library: contexts,
   authentication, configurable transport, structured errors, response limits,
   safe read retries, streaming uploads, and download redirects.
4. Generate deterministic typed resource services and models. Keep v1/v2 in
   separate packages. Preserve omitted, null, empty, and zero values.
5. Add reusable cursor iteration and examples that compile with the library.
6. Exercise every operation against an in-memory HTTP transport. Cross-check
   methods, paths, parameters, bodies, and fixtures with the captured contracts.
7. Independently review correctness, then fix findings and run formatting,
   vet, race tests, coverage, and generation reproducibility checks.
8. Document usage, architecture, maintenance, source limitations, and endpoint
   coverage. Commit with the repository owner's configured identity.

No live API calls are needed for the automated suite. Tests must never consume
Devin credits or depend on credentials.
