package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"

	"github.com/martinlofranodeoliveira/health-probe-cli/probe"
)

// Demo summaries intentionally exclude elapsed time. They are projections of
// checked real reports, not full reports with invented timing measurements.
type demoSummary struct {
	Case string `json:"case"`
	Healthy bool `json:"healthy"`
	StatusCode int `json:"status_code"`
	BytesRead int64 `json:"bytes_read"`
	Reason probe.Reason `json:"reason"`
}

func runDemo(ctx context.Context, out, diagnostics io.Writer) int {
	healthy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		_, _ = io.WriteString(w, "ok")
	}))
	defer healthy.Close()
	failing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(503)
		_, _ = io.WriteString(w, "down")
	}))
	defer failing.Close()

	cases := []struct {
		name string
		url string
		status int
		bytes int64
		healthy bool
		reason probe.Reason
	}{
		{"healthy", healthy.URL, 200, 2, true, probe.ReasonOK},
		{"synthetic-failure", failing.URL, 503, 4, false, probe.ReasonStatusMismatch},
	}
	summaries := make([]demoSummary, 0, len(cases))
	for _, tc := range cases {
		cfg := probe.DefaultConfig(tc.url)
		cfg.TimeoutMS = 10000
		r, err := probe.Run(ctx, cfg)
		if err != nil || r.StatusCode == nil || *r.StatusCode != tc.status || r.Healthy != tc.healthy || r.BytesRead != tc.bytes || r.Reason != tc.reason || r.DurationMS < 0 {
			_, _ = fmt.Fprintln(diagnostics, "health-probe: offline demo check failed")
			return 1
		}
		summaries = append(summaries, demoSummary{tc.name, r.Healthy, *r.StatusCode, r.BytesRead, r.Reason})
	}
	if _, err := fmt.Fprintln(out, "Offline demo: two loopback GETs; durations omitted from summaries."); err != nil {
		return outputFailure(diagnostics)
	}
	encoder := json.NewEncoder(out)
	for _, summary := range summaries {
		if err := encoder.Encode(summary); err != nil {
			return outputFailure(diagnostics)
		}
	}
	if _, err := fmt.Fprintln(out, "Checks passed: 2/2. No external host or persistent state."); err != nil {
		return outputFailure(diagnostics)
	}
	return 0
}
