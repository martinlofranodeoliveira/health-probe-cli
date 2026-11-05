package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type brokenWriter struct{}

func (brokenWriter) Write([]byte) (int, error) {
	return 0, errors.New("invented-output-canary-9a73")
}

func TestOutputFailureIsSanitized(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "ok")
	}))
	defer srv.Close()
	for _, args := range [][]string{{"--help"}, {"demo"}, {srv.URL}} {
		var diagnostics bytes.Buffer
		code := run(context.Background(), args, brokenWriter{}, &diagnostics)
		if code != 1 || diagnostics.String() != "health-probe: could not write output\n" {
			t.Fatalf("output error contract: exit=%d diagnostics=%q", code, diagnostics.String())
		}
		if strings.Contains(diagnostics.String(), "invented-output-canary-9a73") {
			t.Fatal("raw writer error was exposed")
		}
	}
}

func TestParserUpperBoundAndCanonicalNumbers(t *testing.T) {
	args := make([]string, 17)
	for i := range args {
		args[i] = "http://example.invalid/"
	}
	if _, err := parse(args); err == nil {
		t.Fatal("unbounded argument list was accepted")
	}
	inv, err := parse([]string{
		"--timeout-ms=60000", "http://example.invalid/", "--max-bytes", "1048576", "--expect", "200,204",
	})
	if err != nil || inv.config.TimeoutMS != 60000 || inv.config.MaxBytes != 1048576 {
		t.Fatalf("valid boundary arguments: %+v, %v", inv, err)
	}
}
