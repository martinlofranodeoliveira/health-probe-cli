# Design Rationale

## Goals

`health-probe-cli` is designed for scripts, local verification, and CI checks that need a single, predictable HTTP `GET` probe with machine-readable output and strict resource bounds. Its design prioritizes:

1. **Bounded execution:** Every input and network operation has an explicit upper bound.
2. **Sanitized output:** Diagnostics and reports never echo untrusted response payloads, query parameters, credentials, or low-level Go transport strings.
3. **Zero external dependencies:** Implemented strictly with the Go standard library for reproducibility and auditability.
4. **Clear separation between configuration errors and probe outcomes:** Invalid configuration fails before opening a socket (exit code `2` / non-nil Go `error`), while any attempted probe produces a structured `Report`.

## Architecture

The module is split into two packages:

- `probe` (`probe/config.go`, `probe/probe.go`): Reusable library implementing URL and parameter validation, isolated `http.Transport` construction, single-deadline execution, bounded body draining, and outcome classification.
- `main` (`cmd/health-probe`): Thin CLI parser, `--help` output, deterministic `demo` runner using loopback `httptest.Server` instances, and process signal handling (`os.Interrupt`).

## Key Technical Decisions

### Direct `http.RoundTripper` Invocation

Instead of calling `http.Client.Do`, `Prober.Run` invokes `transport.RoundTrip(req)` directly. `http.Client` pre-parses `Location` headers on `3xx` responses even when `CheckRedirect` returns `http.ErrUseLastResponse`, which can turn a redirect response with a malformed `Location` header into a client error. Calling `RoundTrip` directly guarantees that redirects are never followed and that the original `3xx` status code and body are evaluated consistently.

### Single Context Deadline Across All Phases

A single `context.WithTimeout` wraps the caller's `context.Context` before `RoundTrip` is called and remains active while `io.Copy` drains the response body. This ensures `--timeout-ms` bounds DNS resolution, TCP connect, TLS handshake, response header reading, and response body streaming together rather than resetting per phase.

HTTP `101 Switching Protocols` is rejected before body reading. Go transfers the upgraded connection to the response body, so cancellation of the request context no longer closes an idle upgraded stream. Returning `unsupported_upgrade` and closing that body immediately preserves bounded execution; listing `101` as an expected status does not enable protocol upgrades.

### `max-bytes + 1` Streaming Overflow Detection

Response bodies are never buffered in memory. `io.Copy(io.Discard, io.LimitReader(resp.Body, cfg.MaxBytes+1))` streams bytes into a scratch discard buffer and stops as soon as `MaxBytes + 1` bytes have been observed. Reading `MaxBytes` bytes or fewer before `io.EOF` satisfies the limit; reading `MaxBytes + 1` proves the body exceeded `MaxBytes` without downloading the rest of a potentially large stream.

### Fresh, Isolated Transport by Default

When `Prober.Transport` is `nil`, each probe constructs a dedicated `*http.Transport` with:

- `Proxy: nil` (ignores ambient `HTTP_PROXY` / `HTTPS_PROXY` environment variables so probes test the explicit target directly)
- `DisableKeepAlives: true` and `MaxConnsPerHost: 1`
- `DisableCompression: true` and `Accept-Encoding: identity` (measures actual wire body bytes rather than transparently inflating compressed payloads)
- `MaxResponseHeaderBytes: 65536`
- `TLSNextProto` set to an empty map for predictable single-request HTTP/1.1 lifecycle cleanup

For local TLS testing with synthetic self-signed certificates, library callers can inject `httptest.Server.Client().Transport` via `probe.Prober{Transport: ...}` without weakening the CLI's default certificate verification.

### Deterministic Offline Demo

Real probe runs measure wall-clock latency (`duration_ms`) and bind ephemeral loopback ports. In `health-probe demo`, both probes are executed against real loopback `httptest.Server` instances and verified, and the printed demo summaries project the deterministic fields (`case`, `healthy`, `status_code`, `bytes_read`, `reason`) while omitting variable duration and port values rather than fabricating numbers.
