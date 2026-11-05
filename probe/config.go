// Package probe implements bounded, one-shot HTTP GET probes.
package probe

import (
	"errors"
	"net"
	"net/url"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	DefaultTimeoutMS = 2000
	MaxTimeoutMS = 60000
	DefaultMaxBytes int64 = 65536
	MaxBodyBytes int64 = 1048576
	MaxResponseHeaderBytes int64 = 65536
	MaxURLBytes = 8192
	MaxExpectedStatuses = 32
)

// Validation errors deliberately contain no input values.
var (
	ErrInvalidURL = errors.New("invalid URL: require an absolute http/https URL without credentials or fragment")
	ErrInvalidExpected = errors.New("expected statuses must contain 1..32 distinct codes in 100..599")
	ErrInvalidTimeout = errors.New("timeout-ms must be in 1..60000")
	ErrInvalidMaxBytes = errors.New("max-bytes must be in 0..1048576")
	ErrNilContext = errors.New("context must not be nil")
)

// Config is explicit: its zero value is invalid. Use DefaultConfig first.
// Do not mutate Config or its slice while a probe is running.
type Config struct {
	URL string
	ExpectedStatuses []int
	TimeoutMS int
	MaxBytes int64
}

// DefaultConfig returns independent defaults, including a new status slice.
func DefaultConfig(rawURL string) Config {
	return Config{
		URL: rawURL,
		ExpectedStatuses: []int{200},
		TimeoutMS: DefaultTimeoutMS,
		MaxBytes: DefaultMaxBytes,
	}
}

// Validate checks configuration without resolving a host or opening a connection.
func (c Config) Validate() error {
	_, _, err := c.validated()
	return err
}

func (c Config) validated() (*url.URL, []int, error) {
	if len(c.URL) == 0 || len(c.URL) > MaxURLBytes || !utf8.ValidString(c.URL) {
		return nil, nil, ErrInvalidURL
	}
	if !strings.HasPrefix(c.URL, "http://") && !strings.HasPrefix(c.URL, "https://") {
		return nil, nil, ErrInvalidURL
	}
	if strings.Contains(c.URL, "#") {
		return nil, nil, ErrInvalidURL
	}
	for _, r := range c.URL {
		if unicode.IsSpace(r) || unicode.IsControl(r) {
			return nil, nil, ErrInvalidURL
		}
	}
	u, err := url.Parse(c.URL)
	if err != nil || u.Opaque != "" || u.User != nil || u.Hostname() == "" {
		return nil, nil, ErrInvalidURL
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, nil, ErrInvalidURL
	}
	if strings.Contains(u.Host, "%") || strings.HasSuffix(u.Host, ":") {
		return nil, nil, ErrInvalidURL
	}
	if strings.HasPrefix(u.Host, "[") {
		// Support bracketed IPv6, not zone identifiers or IPvFuture syntax.
		if !strings.Contains(u.Hostname(), ":") || net.ParseIP(u.Hostname()) == nil {
			return nil, nil, ErrInvalidURL
		}
	} else if strings.Contains(u.Hostname(), ":") {
		return nil, nil, ErrInvalidURL
	}
	if port := u.Port(); port != "" {
		if port[0] == '0' {
			return nil, nil, ErrInvalidURL
		}
		n, parseErr := strconv.Atoi(port)
		if parseErr != nil || n < 1 || n > 65535 {
			return nil, nil, ErrInvalidURL
		}
	}
	if _, err := url.QueryUnescape(u.RawQuery); err != nil {
		return nil, nil, ErrInvalidURL
	}
	if len(c.ExpectedStatuses) < 1 || len(c.ExpectedStatuses) > MaxExpectedStatuses {
		return nil, nil, ErrInvalidExpected
	}
	seen := make(map[int]bool, len(c.ExpectedStatuses))
	expected := make([]int, len(c.ExpectedStatuses))
	for i, code := range c.ExpectedStatuses {
		if code < 100 || code > 599 || seen[code] {
			return nil, nil, ErrInvalidExpected
		}
		seen[code] = true
		expected[i] = code
	}
	if c.TimeoutMS < 1 || c.TimeoutMS > MaxTimeoutMS {
		return nil, nil, ErrInvalidTimeout
	}
	if c.MaxBytes < 0 || c.MaxBytes > MaxBodyBytes {
		return nil, nil, ErrInvalidMaxBytes
	}
	return u, expected, nil
}
