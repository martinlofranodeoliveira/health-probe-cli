# Changelog

All notable changes to this project are documented in this file.

## [0.1.0] - Unreleased

### Added

- `probe` package providing `Config`, `DefaultConfig`, `Config.Validate`, `Prober`, `Run`, `Report`, and stable `Reason` constants.
- Single-request HTTP/HTTPS `GET` probing with normal TLS verification, redirect refusal, total context deadline (`1..60000` ms), response header cap (`64` KiB), and `max-bytes + 1` body limit detection (`0..1048576` bytes).
- `cmd/health-probe` CLI supporting `--expect`, `--timeout-ms`, `--max-bytes`, `--help`, and `demo`.
- Sanitized JSON reporting and fixed-string error diagnostics that never echo URLs, query parameters, credentials, response bodies, or raw transport errors.
- Deterministic offline `demo` mode using loopback `httptest` servers.
- Unit, boundary, adversarial, and end-to-end CLI binary tests.
