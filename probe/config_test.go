package probe_test

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/martinlofranodeoliveira/health-probe-cli/probe"
)

func TestConfigurationBoundaries(t *testing.T) {
	cfg := probe.DefaultConfig("http://[::1]:8080/health?q=%23")
	cfg.ExpectedStatuses = []int{100, 599}
	cfg.TimeoutMS = 1
	cfg.MaxBytes = 0
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	cfg.TimeoutMS = probe.MaxTimeoutMS
	cfg.MaxBytes = probe.MaxBodyBytes
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	prefix := "https://example.invalid/"
	cfg.URL = prefix + strings.Repeat("x", probe.MaxURLBytes-len(prefix))
	if err := cfg.Validate(); err != nil {
		t.Fatalf("URL at the documented bound was rejected: %v", err)
	}
	cfg.URL += "x"
	if !errors.Is(cfg.Validate(), probe.ErrInvalidURL) {
		t.Fatal("overlong URL was accepted")
	}
	a := probe.DefaultConfig("http://example.invalid/")
	b := probe.DefaultConfig("http://example.invalid/")
	a.ExpectedStatuses[0] = 503
	if b.ExpectedStatuses[0] != 200 {
		t.Fatal("defaults share mutable status storage")
	}
}

func TestInvalidConfigurationNeverUsesTransport(t *testing.T) {
	calls := 0
	p := probe.Prober{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		calls++
		return nil, errors.New("unexpected request")
	})}
	badURLs := []string{
		"", "/relative", "ftp://example.invalid/", "file:///synthetic",
		"HTTP://example.invalid/", "http:///missing", "http://:8080/",
		"http://user:invented-canary-6f21@example.invalid/",
		"http://@example.invalid/", "http://example.invalid/#",
		"http://example.invalid/#invented-canary-6f21",
		"http://example.invalid:0/", "http://example.invalid:080/",
		"http://example.invalid:65536/", "http://example.invalid:/",
		"http://example.invalid/a b", "http://example%20.invalid/",
		"http://example%2f.invalid/", "http://example.invalid/%zz",
		"http://example.invalid/?q=%zz",
		"http://[not-an-ip]/", "http://[fe80::1%25synthetic]/",
	}
	for i, rawURL := range badURLs {
		cfg := probe.DefaultConfig(rawURL)
		_, err := p.Run(context.Background(), cfg)
		if !errors.Is(err, probe.ErrInvalidURL) {
			t.Fatalf("bad URL case %d (%q): got %v", i, rawURL, err)
		}
		if strings.Contains(err.Error(), "invented-canary-6f21") {
			t.Fatal("validation error exposed input")
		}
	}
	cases := []struct {
		name string
		change func(*probe.Config)
		want error
	}{
		{"nil-statuses", func(c *probe.Config) { c.ExpectedStatuses = nil }, probe.ErrInvalidExpected},
		{"empty-statuses", func(c *probe.Config) { c.ExpectedStatuses = []int{} }, probe.ErrInvalidExpected},
		{"duplicate", func(c *probe.Config) { c.ExpectedStatuses = []int{200, 200} }, probe.ErrInvalidExpected},
		{"low-status", func(c *probe.Config) { c.ExpectedStatuses = []int{99} }, probe.ErrInvalidExpected},
		{"high-status", func(c *probe.Config) { c.ExpectedStatuses = []int{600} }, probe.ErrInvalidExpected},
		{"many-statuses", func(c *probe.Config) { c.ExpectedStatuses = make([]int, 33) }, probe.ErrInvalidExpected},
		{"zero-timeout", func(c *probe.Config) { c.TimeoutMS = 0 }, probe.ErrInvalidTimeout},
		{"negative-timeout", func(c *probe.Config) { c.TimeoutMS = -1 }, probe.ErrInvalidTimeout},
		{"large-timeout", func(c *probe.Config) { c.TimeoutMS = 60001 }, probe.ErrInvalidTimeout},
		{"negative-limit", func(c *probe.Config) { c.MaxBytes = -1 }, probe.ErrInvalidMaxBytes},
		{"large-limit", func(c *probe.Config) { c.MaxBytes = 1048577 }, probe.ErrInvalidMaxBytes},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := probe.DefaultConfig("http://example.invalid/")
			tc.change(&cfg)
			if _, err := p.Run(context.Background(), cfg); !errors.Is(err, tc.want) {
				t.Fatalf("got %v, want %v", err, tc.want)
			}
		})
	}
	if _, err := p.Run(nil, probe.DefaultConfig("http://example.invalid/")); !errors.Is(err, probe.ErrNilContext) {
		t.Fatalf("nil context: %v", err)
	}
	if calls != 0 {
		t.Fatalf("invalid configuration made %d requests", calls)
	}
}
