package probe

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"net/http"
	"time"
)

// Reason is a stable machine-readable outcome, never a transport error string.
type Reason string

const (
	ReasonOK                 Reason = "ok"
	ReasonStatusMismatch     Reason = "status_mismatch"
	ReasonBodyLimit          Reason = "body_limit"
	ReasonTimeout            Reason = "timeout"
	ReasonCanceled           Reason = "canceled"
	ReasonRequestError       Reason = "request_error"
	ReasonReadError          Reason = "read_error"
	ReasonUnsupportedUpgrade Reason = "unsupported_upgrade"
)

// Report contains no URL, headers, response content, or raw error messages.
// StatusCode is nil until valid response headers have been received.
type Report struct {
	Healthy    bool   `json:"healthy"`
	StatusCode *int   `json:"status_code"`
	DurationMS int64  `json:"duration_ms"`
	BytesRead  int64  `json:"bytes_read"`
	Reason     Reason `json:"reason"`
}

// Prober permits a trusted transport to be injected, for example from an
// httptest TLS server. A custom transport must honor request cancellation and
// implement the http.RoundTripper contract. Its lifecycle belongs to the caller.
// The zero value uses a fresh, bounded, certificate-verifying transport per run.
type Prober struct {
	Transport http.RoundTripper
}

// Run uses the default Prober. Configuration errors are returned as errors;
// all attempted-probe outcomes, including cancellation, are returned as reports.
func Run(ctx context.Context, cfg Config) (Report, error) {
	return (Prober{}).Run(ctx, cfg)
}

// Run performs one GET without redirect handling. It does not retain the body.
func (p Prober) Run(ctx context.Context, cfg Config) (Report, error) {
	if ctx == nil {
		return Report{}, ErrNilContext
	}
	u, expectedStatuses, err := cfg.validated()
	if err != nil {
		return Report{}, err
	}
	req, err := http.NewRequest(http.MethodGet, u.String(), nil)
	if err != nil {
		return Report{}, ErrInvalidURL
	}

	start := time.Now()
	runCtx, cancel := context.WithTimeout(ctx, time.Duration(cfg.TimeoutMS)*time.Millisecond)
	defer cancel()
	report := Report{}
	finish := func(reason Reason) (Report, error) {
		report.Reason = reason
		report.Healthy = reason == ReasonOK
		report.DurationMS = time.Since(start).Milliseconds()
		return report, nil
	}
	if reason := interruption(runCtx, nil); reason != "" {
		return finish(reason)
	}

	transport := p.Transport
	if transport == nil {
		owned := defaultTransport()
		defer owned.CloseIdleConnections()
		transport = owned
	}
	req = req.WithContext(runCtx)
	req.Close = true
	req.Header.Set("User-Agent", "health-probe/0.1.0")
	req.Header.Set("Accept-Encoding", "identity")

	// RoundTrip, rather than Client.Do, never processes Location headers. This
	// also preserves the original response if a redirect has a malformed URL.
	resp, requestErr := transport.RoundTrip(req)
	if resp != nil && resp.Body != nil {
		defer resp.Body.Close()
	}
	if requestErr != nil {
		if reason := interruption(runCtx, requestErr); reason != "" {
			return finish(reason)
		}
		return finish(ReasonRequestError)
	}
	if resp == nil || resp.Body == nil || resp.StatusCode < 100 || resp.StatusCode > 599 {
		return finish(ReasonRequestError)
	}
	code := resp.StatusCode
	report.StatusCode = &code
	if code == http.StatusSwitchingProtocols {
		// A 101 body owns the upgraded connection: Transport no longer closes
		// it when the request context expires. Reject before any body read;
		// the deferred Close immediately releases that connection on return.
		return finish(ReasonUnsupportedUpgrade)
	}

	// The extra byte distinguishes exactly-at-limit from over-limit. Discard
	// streams through a bounded scratch buffer; no response body is retained.
	n, readErr := io.Copy(io.Discard, io.LimitReader(resp.Body, cfg.MaxBytes+1))
	report.BytesRead = n
	if errors.Is(ctx.Err(), context.Canceled) || errors.Is(readErr, context.Canceled) {
		return finish(ReasonCanceled)
	}
	if readErr != nil {
		if reason := interruption(runCtx, readErr); reason != "" {
			return finish(reason)
		}
		if n > cfg.MaxBytes {
			return finish(ReasonBodyLimit)
		}
		return finish(ReasonReadError)
	}
	if n > cfg.MaxBytes {
		return finish(ReasonBodyLimit)
	}
	for _, expected := range expectedStatuses {
		if resp.StatusCode == expected {
			return finish(ReasonOK)
		}
	}
	return finish(ReasonStatusMismatch)
}

func interruption(ctx context.Context, err error) Reason {
	if errors.Is(ctx.Err(), context.Canceled) || errors.Is(err, context.Canceled) {
		return ReasonCanceled
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) || errors.Is(err, context.DeadlineExceeded) {
		return ReasonTimeout
	}
	var networkErr net.Error
	if errors.As(err, &networkErr) && networkErr.Timeout() {
		return ReasonTimeout
	}
	return ""
}

func defaultTransport() *http.Transport {
	return &http.Transport{
		// No environment proxy, cookie jar, compression, shared connection pool,
		// or HTTP/2 connection reuse. TLS verification uses Go's normal defaults.
		Proxy:                  nil,
		DialContext:            (&net.Dialer{KeepAlive: -1}).DialContext,
		DisableKeepAlives:      true,
		DisableCompression:     true,
		MaxConnsPerHost:        1,
		MaxResponseHeaderBytes: MaxResponseHeaderBytes,
		TLSNextProto:           map[string]func(string, *tls.Conn) http.RoundTripper{},
	}
}
