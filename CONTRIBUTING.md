# Contributing

Thank you for your interest in contributing to `health-probe-cli`.

## Principles

- **Standard library only:** Do not add third-party Go modules.
- **Small, stable surface area:** Preserve the documented CLI flags, exit codes, and JSON report schema in `docs/contract.md`.
- **Sanitization by default:** Never include user-supplied URLs, query parameters, credentials, response headers, response bodies, or raw network error strings in stdout reports or stderr diagnostics.
- **Bounded resource usage:** Every input and network read must remain explicitly bounded.
- **Offline, deterministic tests:** Tests and demos must use loopback `httptest` servers and synthetic fixtures only.

## Development Workflow

Run the full verification suite locally before proposing changes:

```sh
go vet ./...
go test -race ./...
go run ./cmd/health-probe demo
```

## Pull Request Checklist

1. All code is original and licensed under the MIT License.
2. `go vet ./...` and `go test -race ./...` pass without network access outside loopback.
3. Any contract or behavior clarification is reflected in `README.md`, `README.pt-BR.md`, and `docs/contract.md`.
4. Tests cover both valid boundaries and adversarial or malformed inputs.
