# Health Probe CLI

A small, one-shot HTTP checker from **Lofrano Tecnologia**: [official site](https://lofranotecnologia.com.br). Intended repository: [martinlofranodeoliveira/health-probe-cli](https://github.com/martinlofranodeoliveira/health-probe-cli).

It sends one GET to an explicit URL, checks an expected status and a response-body limit, and writes a sanitized JSON report. A total deadline bounds connection establishment, headers, and body reading. It is **not** a continuous monitoring service, a load tester, or a production-readiness assessment.

Original Go code; standard library only. No model/API service dependency, telemetry, credentials, persistent state, or unrelated network requests. All fixtures are synthetic.

## Install, test, and demo

Requires Go 1.27 or newer. From this source directory:

```sh
go test ./...
go build -o health-probe ./cmd/health-probe
go install ./cmd/health-probe
go run ./cmd/health-probe demo
```

`go install` installs into `GOBIN`, or the Go workspace's `bin` directory when `GOBIN` is unset. The local build creates `./health-probe`. There are no third-party modules to download; the matching Go toolchain must already be available for fully offline use.

Additional checks:

```sh
go vet ./...
go test -race ./...
```

Tests start only loopback servers. They exercise the public package and build and execute the actual CLI, including exit codes, TLS rejection, malformed arguments, and sanitized output.

## CLI

For an endpoint that you already run locally:

```sh
./health-probe --expect 200,204 --timeout-ms 1500 --max-bytes 4096 http://127.0.0.1:8080/health
./health-probe --help
```

No endpoint is discovered or started by a normal probe. `--help` includes this local example. Options can appear before or after the URL, using either `--name VALUE` or `--name=VALUE`. Each may appear once. There are no short flags, positional shortcuts, configuration files, environment options, or `--` delimiter. `--help` and `demo` must each stand alone.

| Option | Default | Accepted range |
| --- | --- | --- |
| `--expect` | `200` | 1–32 distinct, comma-separated, three-digit codes in 100–599 |
| `--timeout-ms` | `2000` | 1–60000 milliseconds, total request deadline |
| `--max-bytes` | `65536` | 0–1048576 body bytes |

Integers use canonical decimal digits: no signs, spaces, leading zeroes except `0`, or decimal points. A zero body limit permits an empty response only. URLs are bounded to 8192 UTF-8 bytes and require lowercase `http://` or `https://`, a hostname, and a valid optional port in 1–65535. Credentials, fragments (including an empty `#`), whitespace, control characters, malformed escapes, and IPv6 zone identifiers are rejected. See [the full contract](docs/contract.md).

### Report and exits

An illustrative report for a synthetic 200 response with a two-byte body:

```json
{"healthy":true,"status_code":200,"duration_ms":0,"bytes_read":2,"reason":"ok"}
```

`duration_ms` is measured, truncated elapsed milliseconds, not a benchmark; it varies and can be zero. When no response headers are available, `status_code` is `null`. Bodies, headers, URLs, query values, and raw transport errors never appear in reports or diagnostics. There are no probe diagnostic messages for ordinary unhealthy outcomes: use the JSON reason.

Reasons: `ok`, `status_mismatch`, `body_limit`, `timeout`, `canceled`, `request_error`, `read_error`, `unsupported_upgrade`. DNS, connection, TLS verification, and rejected/oversized response headers share `request_error`. HTTP `101 Switching Protocols` returns `unsupported_upgrade`, with status `101` and zero body bytes, even when `--expect 101` is supplied; the upgraded connection is closed immediately. The probe reads at most `max-bytes + 1`; `bytes_read` is observed body bytes, not Content-Length or the full response size. A matching status is not healthy if the body is excessive, truncated, or times out.

| Exit | Meaning |
| --- | --- |
| 0 | Healthy probe, help, or successful demo |
| 1 | Unhealthy probe, including network/TLS/timeout/cancellation; also demo check or output-write failure |
| 2 | Invalid arguments or configuration; no request |

A normal probe writes exactly one JSON object plus newline to stdout. Invalid input writes a fixed, input-free diagnostic to stderr and no report. An output failure can leave partial stdout and produces a fixed stderr diagnostic when possible. Ctrl+C requests cancellation; forced termination cannot guarantee a report.

## Deterministic offline demo

```sh
go run ./cmd/health-probe demo
```

The demo starts and closes two loopback `httptest` servers, runs real GET probes, and checks both outcomes before printing:

```text
Offline demo: two loopback GETs; durations omitted from summaries.
{"case":"healthy","healthy":true,"status_code":200,"bytes_read":2,"reason":"ok"}
{"case":"synthetic-failure","healthy":false,"status_code":503,"bytes_read":4,"reason":"status_mismatch"}
Checks passed: 2/2. No external host or persistent state.
```

The JSON lines are **demo summaries**, not full probe reports. Measured durations and ephemeral ports are deliberately omitted, rather than replaced with fabricated values, so successful output is deterministic. Each demo probe has a 10000 ms total deadline. Loopback binding must be permitted.

## Reusable package

```go
package main

import (
    "context"
    "encoding/json"
    "os"

    "github.com/martinlofranodeoliveira/health-probe-cli/probe"
)

func main() {
    cfg := probe.DefaultConfig("http://127.0.0.1:8080/health")
    cfg.ExpectedStatuses = []int{200, 204}
    report, err := probe.Run(context.Background(), cfg)
    if err != nil {
        // Only input/configuration errors are returned as errors.
        panic(err)
    }
    if err := json.NewEncoder(os.Stdout).Encode(report); err != nil {
        panic("could not write report")
    }
}
```

Use `Config.Validate()` for network-free validation. `Config`'s zero value is invalid; `DefaultConfig` supplies defaults and an independent status slice. `Run` returns a report with a nil error even for network failures. A nil context is invalid. A caller's earlier deadline or cancellation is respected.

For a locally trusted test TLS server, inject its transport with `probe.Prober{Transport: server.Client().Transport}.Run(ctx, cfg)`. The caller owns that transport and must ensure it honors cancellation and the `http.RoundTripper` contract. The default transport is used when the field is nil. Do not mutate configuration during a run. See [API and JSON details](docs/contract.md).

## Design and limitations

- Fresh HTTP/1.1 transport per default probe; no redirects, HTTP-level retries, shared connection pool, cookies, environment proxies, or automatic decompression. Normal TLS certificate and hostname verification stays enabled. A 302 can be explicitly expected, but its destination is never visited.
- A 64 KiB response-header cap and streaming body discard bound application-level reading. The extra body byte is a deliberate overflow detector. Buffers and TLS/HTTP framing add overhead; this is not a cap on all network bytes or process memory.
- No latency threshold separate from the total timeout, response-content validation, DNS allowlist, authentication, client certificates, custom headers, HEAD mode, or persistent history.
- Availability is a single observation, not an uptime guarantee. Connection/DNS behavior and scheduling affect duration. The deadline is not a hard real-time process-kill guarantee.
- The library's trusted transport hook can change protocol, trust, resource, and retry behavior. Arbitrary injected code cannot be forced to honor a context.
- Explicit URLs may target private or loopback addresses. Do not use this as an SSRF filter or expose it to untrusted callers with privileged network access. Query values are sent to the target and may be visible in process arguments even though reports suppress them.
- This implements a documented subset using Go's HTTP stack; it makes no broad HTTP standards-compliance claim.

Read [design rationale](docs/design.md), [security notes](SECURITY.md), and [contribution guidance](CONTRIBUTING.md). Licensed under [MIT](LICENSE), copyright 2026 Lofrano Tecnologia.
