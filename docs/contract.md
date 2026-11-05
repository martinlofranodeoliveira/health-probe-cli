# Health Probe Contract

This document defines the public CLI and Go package contract for `health-probe-cli` `0.1.0`.

## Scope

`health-probe-cli` performs a single, bounded HTTP `GET` request against one explicit `http` or `https` URL and produces a sanitized JSON report. It is not a continuous monitoring agent, retry wrapper, crawler, or load generator.

## CLI Contract

### Forms

```text
health-probe [--expect CODES] [--timeout-ms N] [--max-bytes N] URL
health-probe --help
health-probe demo
```

- `--help` and `demo` must be passed as the sole argument.
- In probe mode, at most 16 arguments are accepted, and each individual argument is bounded to 8192 UTF-8 bytes.
- Options may appear before or after `URL`, either as `--name VALUE` or `--name=VALUE`.
- Each option (`--expect`, `--timeout-ms`, `--max-bytes`) may appear at most once.
- Unknown flags, short flags (such as `-h`), the `--` delimiter, multiple URLs, or missing option values are rejected with exit code `2` before any network activity occurs.

### Option Grammar and Ranges

| Option | Default | Valid Range | Grammar |
| --- | --- | --- | --- |
| `--expect` | `200` | 1..32 distinct status codes, each in `100..599` | Comma-separated 3-digit canonical integers (`200` or `200,204`) |
| `--timeout-ms` | `2000` | `1..60000` | Canonical base-10 integer (no sign, whitespace, or leading zero) |
| `--max-bytes` | `65536` | `0..1048576` | Canonical base-10 integer (`0` or non-zero without leading zeroes) |

### URL Validation Rules

A target URL is validated before any DNS lookup or socket connection:

- Must be valid UTF-8 and between `1` and `8192` bytes inclusive.
- Must begin with lowercase `http://` or `https://`.
- Must not contain whitespace, control characters, userinfo credentials (`user:pass@` or `@`), or fragments (`#`, including an empty trailing `#`).
- Must have a non-empty hostname.
- Must not contain percent-encoding in the host authority (`u.Host`), trailing colons (`http://example.com:/`), IPv6 zone identifiers, or unbracketed colons in hostnames.
- Bracketed hosts must be valid IPv6 addresses (`net.ParseIP`).
- If an explicit port is present, it must use canonical digits without leading zeroes in `1..65535`.
- Path and query escapes must be valid percent-encodings.

### HTTP Execution Rules

- Method: `GET` only.
- Redirects: never followed. A `3xx` response is evaluated directly against `ExpectedStatuses`.
- Retries: disabled.
- TLS: default `Prober` uses Go's standard certificate chain and hostname verification.
- Headers sent: `User-Agent: health-probe/0.1.0`, `Accept-Encoding: identity`, `Connection: close`.
- Response header limit: `65536` bytes (`MaxResponseHeaderBytes`) on the default transport.
- Protocol upgrades: a `101 Switching Protocols` response is rejected as `unsupported_upgrade`, even when `101` is expected. Its connection is closed immediately without reading upgraded data; `status_code` is `101` and `bytes_read` is `0`.
- Response body limit: reads at most `max-bytes + 1` bytes into `io.Discard` without retaining body content.
- Deadline: one `context.WithTimeout` deadline covers connection establishment, TLS handshake, response headers, and body reading.

### JSON Report Schema

A completed probe writes one JSON object followed by `\n` to stdout:

```json
{"healthy":true,"status_code":200,"duration_ms":0,"bytes_read":2,"reason":"ok"}
```

| Field | JSON Type | Guarantee |
| --- | --- | --- |
| `healthy` | `boolean` | `true` if and only if `reason` is `"ok"` |
| `status_code` | `number` or `null` | Integer in `100..599` once valid response headers are received; otherwise `null` |
| `duration_ms` | `number` | Non-negative truncated elapsed milliseconds (`>= 0`) |
| `bytes_read` | `number` | Body bytes read before completion, error, or overflow detection (`0..max-bytes+1`) |
| `reason` | `string` | One of the stable `probe.Reason` constants |

### Stable Reason Codes

| Reason | Meaning |
| --- | --- |
| `ok` | Response body was read to EOF within `max-bytes` and `status_code` matched `ExpectedStatuses` |
| `status_mismatch` | Response body was read to EOF within `max-bytes`, but `status_code` was not in `ExpectedStatuses` |
| `body_limit` | Response body exceeded `max-bytes` (`bytes_read == max-bytes + 1`), regardless of `status_code` |
| `timeout` | Total deadline expired or a network timeout occurred during request or body read |
| `canceled` | Caller context or process interrupt canceled the probe |
| `request_error` | DNS, TCP, TLS, header size, or protocol failure before valid response headers were received |
| `read_error` | Mid-stream connection reset, unexpected EOF, or framing error while reading the response body |
| `unsupported_upgrade` | A `101 Switching Protocols` response was received; the upgraded connection was closed without reading it, regardless of `ExpectedStatuses` |

### Exit Codes and Sanitization

| Exit Code | Condition | Stdout | Stderr |
| --- | --- | --- | --- |
| `0` | Healthy probe (`reason == "ok"`), `--help`, or successful `demo` | JSON report, help text, or demo output | Empty |
| `1` | Unhealthy probe (`reason != "ok"`), failed demo check, or stdout write error | JSON report (for completed unhealthy probes) | Empty for normal unhealthy probes; fixed diagnostic on write/demo failure |
| `2` | Invalid CLI arguments or invalid configuration | Empty | Fixed, input-free diagnostic message |

No response body, response header, URL, query parameter value, credential, or raw `net/http` / OS error string is ever written to stdout or stderr.

## Go Package Contract (`probe`)

```go
package probe

type Config struct {
	URL              string
	ExpectedStatuses []int
	TimeoutMS        int
	MaxBytes         int64
}

func DefaultConfig(rawURL string) Config
func (c Config) Validate() error
func Run(ctx context.Context, cfg Config) (Report, error)

type Prober struct {
	Transport http.RoundTripper
}

func (p Prober) Run(ctx context.Context, cfg Config) (Report, error)
```

- `DefaultConfig(rawURL)` initializes `ExpectedStatuses: []int{200}`, `TimeoutMS: 2000`, `MaxBytes: 65536`.
- `Validate()` returns one of `ErrInvalidURL`, `ErrInvalidExpected`, `ErrInvalidTimeout`, or `ErrInvalidMaxBytes` when invalid.
- `Run` returns a non-nil `error` only for `ErrNilContext` or configuration validation errors. Network, TLS, timeout, cancellation, body-limit, and status failures return `err == nil` and a populated `Report`.
