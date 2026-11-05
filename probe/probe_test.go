package probe_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/martinlofranodeoliveira/health-probe-cli/probe"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func TestStatusAndBodyBoundaries(t *testing.T) {
	cases := []struct {
		name     string
		status   int
		body     string
		expected []int
		limit    int64
		healthy  bool
		reason   probe.Reason
		bytes    int64
	}{
		{"matching", 200, "abc", []int{200}, 8, true, probe.ReasonOK, 3},
		{"multiple-expected", 201, "abc", []int{200, 201}, 8, true, probe.ReasonOK, 3},
		{"mismatch", 503, "down", []int{200}, 8, false, probe.ReasonStatusMismatch, 4},
		{"empty-204", 204, "", []int{204}, 0, true, probe.ReasonOK, 0},
		{"exact-limit", 200, "abcd", []int{200}, 4, true, probe.ReasonOK, 4},
		{"one-over", 200, "abcde", []int{200}, 4, false, probe.ReasonBodyLimit, 5},
		{"far-over", 200, strings.Repeat("x", 4096), []int{200}, 4, false, probe.ReasonBodyLimit, 5},
		{"zero-limit", 200, "x", []int{200}, 0, false, probe.ReasonBodyLimit, 1},
		{"limit-before-status", 503, "abcde", []int{200}, 4, false, probe.ReasonBodyLimit, 5},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int64
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.Method != http.MethodGet {
					t.Errorf("method = %s", r.Method)
				}
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, tc.body)
			}))
			defer srv.Close()
			cfg := probe.DefaultConfig(srv.URL)
			cfg.ExpectedStatuses = tc.expected
			cfg.MaxBytes = tc.limit
			got, err := probe.Run(context.Background(), cfg)
			if err != nil {
				t.Fatal(err)
			}
			if got.Healthy != tc.healthy || got.Reason != tc.reason || got.BytesRead != tc.bytes {
				t.Fatalf("unexpected outcome: %+v", got)
			}
			if got.StatusCode == nil || *got.StatusCode != tc.status || got.DurationMS < 0 {
				t.Fatalf("invalid status or duration: %+v", got)
			}
			if calls.Load() != 1 {
				t.Fatalf("made %d GETs", calls.Load())
			}
		})
	}
}

func TestRedirectsAreResponsesEvenWithMalformedLocation(t *testing.T) {
	for _, location := range []string{"/target", "http://[malformed"} {
		t.Run(location, func(t *testing.T) {
			var calls atomic.Int64
			var targetCalls atomic.Int64
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.URL.Path == "/target" {
					targetCalls.Add(1)
				}
				w.Header().Set("Location", location)
				w.WriteHeader(http.StatusFound)
			}))
			defer srv.Close()
			cfg := probe.DefaultConfig(srv.URL)
			got, err := probe.Run(context.Background(), cfg)
			if err != nil || got.Reason != probe.ReasonStatusMismatch || got.StatusCode == nil || *got.StatusCode != 302 {
				t.Fatalf("redirect result: %+v, %v", got, err)
			}
			cfg.ExpectedStatuses = []int{302}
			got, err = probe.Run(context.Background(), cfg)
			if err != nil || !got.Healthy {
				t.Fatalf("explicitly expected redirect: %+v, %v", got, err)
			}
			if calls.Load() != 2 || targetCalls.Load() != 0 {
				t.Fatal("a redirect was followed or a request retried")
			}
		})
	}
}

func TestTLSVerificationAndTrustedInjection(t *testing.T) {
	var calls atomic.Int64
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(204)
	}))
	srv.Config.ErrorLog = log.New(io.Discard, "", 0)
	srv.StartTLS()
	defer srv.Close()
	cfg := probe.DefaultConfig(srv.URL)
	cfg.ExpectedStatuses = []int{204}
	got, err := probe.Run(context.Background(), cfg)
	if err != nil || got.Healthy || got.Reason != probe.ReasonRequestError || got.StatusCode != nil {
		t.Fatalf("untrusted TLS was not rejected: %+v, %v", got, err)
	}
	trusted := probe.Prober{Transport: srv.Client().Transport}
	got, err = trusted.Run(context.Background(), cfg)
	if err != nil || !got.Healthy || got.BytesRead != 0 {
		t.Fatalf("trusted injected transport failed: %+v, %v", got, err)
	}
	if calls.Load() != 1 {
		t.Fatalf("handler received %d requests", calls.Load())
	}
}

func blockingBodyServer(t *testing.T) (*httptest.Server, <-chan struct{}) {
	t.Helper()
	started := make(chan struct{})
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		_, _ = io.WriteString(w, "x")
		w.(http.Flusher).Flush()
		close(started)
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}))
	t.Cleanup(srv.Close)
	t.Cleanup(func() { close(release) })
	return srv, started
}

func TestTotalTimeoutIncludesBodyReading(t *testing.T) {
	srv, _ := blockingBodyServer(t)
	cfg := probe.DefaultConfig(srv.URL)
	cfg.TimeoutMS = 300
	parent, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	start := time.Now()
	got, err := probe.Run(parent, cfg)
	elapsed := time.Since(start)
	if err != nil || got.Healthy || got.Reason != probe.ReasonTimeout {
		t.Fatalf("body timeout: %+v, %v", got, err)
	}
	if got.StatusCode == nil || *got.StatusCode != 200 {
		t.Fatalf("did not receive flushed headers before timeout: %+v", got)
	}
	if elapsed > 3*time.Second || got.DurationMS < 0 {
		t.Fatalf("deadline was not bounded: %v", elapsed)
	}
}

func TestIdleProtocolUpgradeIsRejectedBeforeTimeoutOrCancellation(t *testing.T) {
	for _, mode := range []string{"deadline", "cancellation"} {
		t.Run(mode, func(t *testing.T) {
			connections := make(chan net.Conn, 1)
			peerClosed := make(chan error, 1)
			var calls atomic.Int64
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				conn, buffered, err := w.(http.Hijacker).Hijack()
				if err != nil {
					t.Errorf("hijack failed: %v", err)
					return
				}
				defer conn.Close()
				_, err = buffered.WriteString("HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\nUpgrade: synthetic-idle\r\n\r\n")
				if err != nil || buffered.Flush() != nil {
					return
				}
				connections <- conn
				// Keep the upgraded stream idle and open until the client closes
				// it. Cleanup closes this hijacked conn even if the probe regresses.
				var b [1]byte
				_, err = conn.Read(b[:])
				peerClosed <- err
			}))
			t.Cleanup(srv.Close)
			cfg := probe.DefaultConfig(srv.URL)
			cfg.ExpectedStatuses = []int{http.StatusSwitchingProtocols}
			cfg.MaxBytes = 0
			cfg.TimeoutMS = 500
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			p := probe.Prober{}
			headersReceived := make(chan struct{}, 1)
			if mode == "cancellation" {
				cfg.TimeoutMS = 60000
				transport := &http.Transport{Proxy: nil, DisableKeepAlives: true}
				t.Cleanup(transport.CloseIdleConnections)
				p.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
					resp, err := transport.RoundTrip(r)
					if err == nil && resp.StatusCode == http.StatusSwitchingProtocols {
						headersReceived <- struct{}{}
					}
					return resp, err
				})
			}
			type result struct {
				report probe.Report
				err    error
			}
			done := make(chan result, 1)
			go func() {
				report, err := p.Run(ctx, cfg)
				done <- result{report, err}
			}()
			select {
			case conn := <-connections:
				t.Cleanup(func() { _ = conn.Close() })
			case <-time.After(3 * time.Second):
				t.Fatal("local server never flushed the protocol upgrade")
			}
			if mode == "cancellation" {
				select {
				case <-headersReceived:
					cancel()
				case <-time.After(3 * time.Second):
					t.Fatal("transport never received the protocol upgrade")
				}
			}
			select {
			case result := <-done:
				got := result.report
				if result.err != nil || got.Healthy || got.Reason != probe.ReasonUnsupportedUpgrade || got.BytesRead != 0 || got.StatusCode == nil || *got.StatusCode != 101 {
					t.Fatalf("upgrade was not rejected without reading: %+v, %v", got, result.err)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("idle upgraded connection blocked despite deadline or cancellation")
			}
			select {
			case err := <-peerClosed:
				if !errors.Is(err, io.EOF) {
					t.Fatalf("upgrade connection did not close cleanly: %v", err)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("rejected upgrade connection remained open")
			}
			if calls.Load() != 1 {
				t.Fatalf("made %d requests", calls.Load())
			}
		})
	}
}

func TestProtocolUpgradeClosesBodyWithoutReadingRegardlessOfExpectedStatus(t *testing.T) {
	for _, expected := range []int{200, 101} {
		body := &unreadUpgradeBody{}
		p := probe.Prober{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 101, Body: body}, nil
		})}
		cfg := probe.DefaultConfig("http://example.invalid/")
		cfg.ExpectedStatuses = []int{expected}
		got, err := p.Run(context.Background(), cfg)
		if err != nil || got.Reason != probe.ReasonUnsupportedUpgrade || got.BytesRead != 0 || body.read || !body.closed {
			t.Fatalf("upgrade outcome: %+v, %v, read=%v, closed=%v", got, err, body.read, body.closed)
		}
	}
}

type unreadUpgradeBody struct {
	read   bool
	closed bool
}

func (b *unreadUpgradeBody) Read([]byte) (int, error) {
	b.read = true
	return 0, errors.New("upgraded body must not be read")
}

func (b *unreadUpgradeBody) Close() error {
	b.closed = true
	return nil
}

func TestCancellationBeforeAndDuringRequest(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	calls := 0
	p := probe.Prober{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		calls++
		return nil, errors.New("unexpected call")
	})}
	got, err := p.Run(ctx, probe.DefaultConfig("http://example.invalid/"))
	if err != nil || got.Reason != probe.ReasonCanceled || got.StatusCode != nil || calls != 0 {
		t.Fatalf("pre-cancellation: %+v, %v, calls=%d", got, err, calls)
	}

	srv, started := blockingBodyServer(t)
	ctx, cancel = context.WithCancel(context.Background())
	defer cancel()
	cfg := probe.DefaultConfig(srv.URL)
	cfg.TimeoutMS = 60000
	type result struct {
		report probe.Report
		err    error
	}
	done := make(chan result, 1)
	go func() {
		r, e := probe.Run(ctx, cfg)
		done <- result{r, e}
	}()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("request never reached the local handler")
	}
	cancel()
	select {
	case r := <-done:
		if r.err != nil || r.report.Healthy || r.report.Reason != probe.ReasonCanceled {
			t.Fatalf("in-flight cancellation: %+v, %v", r.report, r.err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("cancellation did not interrupt body reading")
	}
}

func TestTruncatedBodyAndOversizedHeaders(t *testing.T) {
	t.Run("truncated-body", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Length", "10")
			_, _ = io.WriteString(w, "abc")
		}))
		defer srv.Close()
		got, err := probe.Run(context.Background(), probe.DefaultConfig(srv.URL))
		if err != nil || got.Reason != probe.ReasonReadError || got.BytesRead != 3 || got.StatusCode == nil {
			t.Fatalf("truncated response: %+v, %v", got, err)
		}
	})
	t.Run("header-cap", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("X-Synthetic", strings.Repeat("x", 70*1024))
			w.WriteHeader(200)
		}))
		defer srv.Close()
		got, err := probe.Run(context.Background(), probe.DefaultConfig(srv.URL))
		if err != nil || got.Healthy || got.Reason != probe.ReasonRequestError || got.StatusCode != nil || got.BytesRead != 0 {
			t.Fatalf("oversized headers: %+v, %v", got, err)
		}
	})
}

func TestMalformedTransportResponsesLeaveStatusCodeNil(t *testing.T) {
	closed := false
	body := &trackingCloser{Reader: strings.NewReader("abc"), closed: &closed}
	cases := []struct {
		name string
		resp *http.Response
		err  error
	}{
		{"resp-with-error", &http.Response{StatusCode: 200, Body: body}, errors.New("synthetic failure")},
		{"nil-body", &http.Response{StatusCode: 200, Body: nil}, nil},
		{"low-status", &http.Response{StatusCode: 0, Body: http.NoBody}, nil},
		{"high-status", &http.Response{StatusCode: 999, Body: http.NoBody}, nil},
	}
	cfg := probe.DefaultConfig("http://example.invalid/")
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := probe.Prober{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				return tc.resp, tc.err
			})}
			got, err := p.Run(context.Background(), cfg)
			if err != nil || got.Healthy || got.Reason != probe.ReasonRequestError || got.StatusCode != nil {
				t.Fatalf("unexpected report for %s: %+v, %v", tc.name, got, err)
			}
		})
	}
	if !closed {
		t.Fatal("response body was not closed when RoundTrip returned both response and error")
	}
}

type trackingCloser struct {
	io.Reader
	closed *bool
}

func (t *trackingCloser) Close() error {
	*t.closed = true
	return nil
}

func TestReportsDoNotExposeBodyQueryOrTransportErrors(t *testing.T) {
	const sentinel = "invented-canary-6f21"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, sentinel)
	}))
	defer srv.Close()
	cfg := probe.DefaultConfig(srv.URL + "/?synthetic=" + sentinel)
	for _, limit := range []int64{2, 65536} {
		cfg.MaxBytes = limit
		r, err := probe.Run(context.Background(), cfg)
		if err != nil {
			t.Fatal(err)
		}
		assertNoSentinel(t, r, sentinel)
	}
	p := probe.Prober{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("transport failed for ?synthetic=" + sentinel)
	})}
	r, err := p.Run(context.Background(), cfg)
	if err != nil || r.Reason != probe.ReasonRequestError || r.StatusCode != nil {
		t.Fatalf("transport failure: %+v, %v", r, err)
	}
	assertNoSentinel(t, r, sentinel)
}

func assertNoSentinel(t *testing.T, r probe.Report, sentinel string) {
	t.Helper()
	encoded, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), sentinel) {
		t.Fatal("report exposed synthetic sensitive input")
	}
}
