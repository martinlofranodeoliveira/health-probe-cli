package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/martinlofranodeoliveira/health-probe-cli/probe"
)

func invoke(t *testing.T, binary string, args ...string) (int, string, string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, args...)
	var out, diagnostics bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &diagnostics
	err := cmd.Run()
	if ctx.Err() != nil {
		t.Fatal("CLI exceeded test deadline")
	}
	code := 0
	if err != nil {
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) {
			t.Fatal(err)
		}
		code = exitErr.ExitCode()
	}
	return code, out.String(), diagnostics.String()
}

func decodeReport(t *testing.T, text string) probe.Report {
	t.Helper()
	var fields map[string]json.RawMessage
	if err := json.Unmarshal([]byte(text), &fields); err != nil {
		t.Fatalf("stdout is not one JSON object: %v", err)
	}
	if len(fields) != 5 {
		t.Fatalf("unexpected report fields: %s", text)
	}
	for _, key := range []string{"healthy", "status_code", "duration_ms", "bytes_read", "reason"} {
		if _, ok := fields[key]; !ok {
			t.Fatalf("missing field %s", key)
		}
	}
	var report probe.Report
	if err := json.Unmarshal([]byte(text), &report); err != nil {
		t.Fatal(err)
	}
	if report.DurationMS < 0 {
		t.Fatal("negative duration")
	}
	return report
}

func TestActualCLI(t *testing.T) {
	name := "health-probe"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	binary := filepath.Join(t.TempDir(), name)
	buildCtx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	if output, err := exec.CommandContext(buildCtx, "go", "build", "-o", binary, ".").CombinedOutput(); err != nil {
		t.Fatalf("build CLI: %v\n%s", err, output)
	}

	const sentinel = "invented-canary-6f21"
	var calls atomic.Int64
	var targetCalls atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		switch r.URL.Path {
		case "/empty":
			w.WriteHeader(204)
		case "/failure":
			w.WriteHeader(503)
			_, _ = io.WriteString(w, sentinel)
		case "/overflow":
			_, _ = io.WriteString(w, sentinel)
		case "/redirect":
			w.Header().Set("Location", "/target")
			w.WriteHeader(302)
		case "/target":
			targetCalls.Add(1)
			w.WriteHeader(200)
		default:
			_, _ = io.WriteString(w, "ok")
		}
	}))
	defer srv.Close()

	invalid := [][]string{
		{}, {srv.URL, srv.URL}, {"--unknown=" + sentinel, srv.URL},
		{"--timeout-ms", "0", srv.URL}, {"--timeout-ms=-1", srv.URL},
		{"--timeout-ms=60001", srv.URL}, {"--timeout-ms=+1", srv.URL},
		{"--timeout-ms=01", srv.URL}, {"--timeout-ms=1.5", srv.URL},
		{"--max-bytes=-1", srv.URL}, {"--max-bytes=1048577", srv.URL},
		{"--max-bytes=999999999999999999999", srv.URL},
		{"--expect=", srv.URL}, {"--expect=200,200", srv.URL},
		{"--expect=99", srv.URL}, {"--expect=600", srv.URL},
		{"--expect=200,", srv.URL}, {"--expect=200, 204", srv.URL},
		{"--expect=" + strings.Repeat("200,", 32) + "201", srv.URL},
		{"--expect=200", "--expect=204", srv.URL},
		{"--timeout-ms"}, {"-h"}, {"--", srv.URL},
		{"--help", srv.URL}, {"demo", srv.URL}, {"relative"},
		{"ftp://example.invalid/"}, {srv.URL + "#"},
		{"http://synthetic:" + sentinel + "@127.0.0.1/"},
		{srv.URL + "/?synthetic=" + sentinel, "--bad"},
		{srv.URL + "/" + strings.Repeat("x", probe.MaxURLBytes)},
	}
	for i, args := range invalid {
		code, out, diagnostics := invoke(t, binary, args...)
		if code != 2 || out != "" || diagnostics == "" {
			t.Fatalf("invalid case %d: exit=%d stdout=%q stderr=%q", i, code, out, diagnostics)
		}
		if strings.Contains(diagnostics, sentinel) || strings.Contains(diagnostics, srv.URL) {
			t.Fatalf("invalid case %d leaked input", i)
		}
	}
	if calls.Load() != 0 {
		t.Fatal("malformed input caused a request")
	}

	cases := []struct {
		name string
		args []string
		code int
		status int
		healthy bool
		reason probe.Reason
		bytes int64
	}{
		{"healthy", []string{srv.URL + "/?synthetic=" + sentinel}, 0, 200, true, probe.ReasonOK, 2},
		{"mismatch", []string{srv.URL + "/failure?synthetic=" + sentinel}, 1, 503, false, probe.ReasonStatusMismatch, int64(len(sentinel))},
		{"empty", []string{"--expect=204", "--max-bytes=0", srv.URL + "/empty"}, 0, 204, true, probe.ReasonOK, 0},
		{"body-limit", []string{srv.URL + "/overflow?synthetic=" + sentinel, "--max-bytes", "1"}, 1, 200, false, probe.ReasonBodyLimit, 2},
		{"redirect", []string{srv.URL + "/redirect"}, 1, 302, false, probe.ReasonStatusMismatch, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			before := calls.Load()
			code, out, diagnostics := invoke(t, binary, tc.args...)
			if code != tc.code || diagnostics != "" {
				t.Fatalf("exit=%d stderr=%q", code, diagnostics)
			}
			if strings.Contains(out+diagnostics, sentinel) {
				t.Fatal("CLI exposed body or query")
			}
			r := decodeReport(t, out)
			if r.Healthy != tc.healthy || r.Reason != tc.reason || r.BytesRead != tc.bytes || r.StatusCode == nil || *r.StatusCode != tc.status {
				t.Fatalf("unexpected CLI report: %s", out)
			}
			if calls.Load()-before != 1 {
				t.Fatal("CLI did not make exactly one GET")
			}
		})
	}
	if targetCalls.Load() != 0 {
		t.Fatal("CLI followed a redirect")
	}

	t.Run("untrusted-tls", func(t *testing.T) {
		tlsServer := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			t.Error("untrusted TLS request reached HTTP handler")
		}))
		tlsServer.Config.ErrorLog = log.New(io.Discard, "", 0)
		tlsServer.StartTLS()
		defer tlsServer.Close()
		code, out, diagnostics := invoke(t, binary, tlsServer.URL+"/?synthetic="+sentinel)
		r := decodeReport(t, out)
		if code != 1 || diagnostics != "" || r.StatusCode != nil || r.Reason != probe.ReasonRequestError || r.Healthy {
			t.Fatalf("TLS failure contract: exit=%d, %s, %q", code, out, diagnostics)
		}
		if strings.Contains(out+diagnostics, sentinel) {
			t.Fatal("TLS diagnostic leaked query")
		}
	})

	t.Run("help", func(t *testing.T) {
		code, out, diagnostics := invoke(t, binary, "--help")
		if code != 0 || diagnostics != "" || !strings.Contains(out, "http://127.0.0.1:8080/health") {
			t.Fatal("help lacks a usable local example")
		}
	})
	t.Run("offline-demo", func(t *testing.T) {
		code, out, diagnostics := invoke(t, binary, "demo")
		const expected = `Offline demo: two loopback GETs; durations omitted from summaries.
{"case":"healthy","healthy":true,"status_code":200,"bytes_read":2,"reason":"ok"}
{"case":"synthetic-failure","healthy":false,"status_code":503,"bytes_read":4,"reason":"status_mismatch"}
Checks passed: 2/2. No external host or persistent state.
`
		if code != 0 || diagnostics != "" || out != expected {
			t.Fatalf("demo: exit=%d stdout=%q stderr=%q", code, out, diagnostics)
		}
	})
}
