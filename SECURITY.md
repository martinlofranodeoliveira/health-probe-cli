# Security Policy

## Security Design Notes

`health-probe-cli` is a one-shot HTTP probe utility with deliberate security boundaries:

- **Input validation before network I/O:** URLs with credentials (`user:pass@`), fragments (`#`), non-`http`/`https` schemes, percent-encoded hostnames, IPv6 zone identifiers, whitespace, or control characters are rejected before any DNS lookup or socket connection.
- **TLS verification:** Default probes always verify TLS certificate chains and hostnames using the system trust store.
- **Bounded reads:** Response headers are capped at 64 KiB on the default transport, and response bodies are read through `io.LimitReader(resp.Body, maxBytes+1)` into `io.Discard`.
- **Sanitized output:** JSON reports and stderr diagnostics never include response bodies, response headers, query parameter values, credentials, or raw Go transport errors.

## Operational Threat Model and Limitations

- **SSRF:** This tool probes the explicit URL provided by its caller, including loopback (`127.0.0.1`, `[::1]`) and RFC 1918 private addresses. Do **not** expose `health-probe` or `probe.Run` to untrusted users who can supply arbitrary target URLs from a privileged network environment.
- **Process arguments:** While query strings are never printed in reports or diagnostics, command-line arguments passed to `health-probe` may be visible to other local users via the operating system's process table (`ps` / `/proc`). Avoid passing secrets in URLs.
- **Resource bounds:** Application-level header and body reads are bounded, but TLS handshakes and kernel socket buffers still consume per-invocation memory and file descriptors.

## Reporting a Vulnerability

Please report suspected security issues privately to **Lofrano Tecnologia** via the contact channels listed at [https://lofranotecnologia.com.br](https://lofranotecnologia.com.br) rather than opening a public issue with exploit details. Include a minimal reproduction using synthetic inputs only.
