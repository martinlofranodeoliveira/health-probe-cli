package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/martinlofranodeoliveira/health-probe-cli/probe"
)

const usage = `Health Probe CLI 0.1.0
One explicit URL, one GET, no redirects or retries.

Usage:
  health-probe [--expect CODES] [--timeout-ms N] [--max-bytes N] URL
  health-probe --help
  health-probe demo

Options may precede or follow the URL; --name=value is also accepted.
Each option may appear once. No short flags or -- delimiter are supported.
  --expect       1..32 distinct comma-separated codes, 100..599 (default 200)
  --timeout-ms   total deadline, 1..60000 milliseconds (default 2000)
  --max-bytes    body limit, 0..1048576 bytes (default 65536)

Local example (requires your own endpoint):
  health-probe --expect 200,204 --timeout-ms 1500 http://127.0.0.1:8080/health
Offline example:
  health-probe demo

Probe stdout: one JSON object. No URL, body, or raw transport error is printed.
Exit: 0 healthy/help/successful demo; 1 unhealthy or output/demo failure;
      2 invalid arguments/configuration.
`

var errArguments = errors.New("invalid arguments; use --help")

type invocation struct {
	mode string
	config probe.Config
}

func parse(args []string) (invocation, error) {
	if len(args) == 1 && args[0] == "--help" {
		return invocation{mode: "help"}, nil
	}
	if len(args) == 1 && args[0] == "demo" {
		return invocation{mode: "demo"}, nil
	}
	if len(args) == 0 || len(args) > 16 {
		return invocation{}, errArguments
	}
	cfg := probe.DefaultConfig("")
	seen := make(map[string]bool, 3)
	hasURL := false
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if len(arg) > probe.MaxURLBytes {
			return invocation{}, errArguments
		}
		if strings.HasPrefix(arg, "--") {
			name, value, hasValue := strings.Cut(arg, "=")
			if name != "--expect" && name != "--timeout-ms" && name != "--max-bytes" {
				return invocation{}, errArguments
			}
			if seen[name] {
				return invocation{}, errArguments
			}
			seen[name] = true
			if !hasValue {
				i++
				if i >= len(args) {
					return invocation{}, errArguments
				}
				value = args[i]
			}
			switch name {
			case "--expect":
				codes, ok := expectedCodes(value)
				if !ok {
					return invocation{}, errArguments
				}
				cfg.ExpectedStatuses = codes
			case "--timeout-ms":
				n, ok := decimal(value)
				if !ok {
					return invocation{}, errArguments
				}
				cfg.TimeoutMS = n
			case "--max-bytes":
				n, ok := decimal(value)
				if !ok {
					return invocation{}, errArguments
				}
				cfg.MaxBytes = int64(n)
			}
			continue
		}
		if strings.HasPrefix(arg, "-") || hasURL {
			return invocation{}, errArguments
		}
		cfg.URL = arg
		hasURL = true
	}
	if !hasURL {
		return invocation{}, errArguments
	}
	if err := cfg.Validate(); err != nil {
		return invocation{}, err
	}
	return invocation{mode: "probe", config: cfg}, nil
}

// A small canonical decimal grammar avoids signs, whitespace, overflow, and
// arbitrarily long numeric inputs. Range checking belongs to Config.Validate.
func decimal(s string) (int, bool) {
	if len(s) == 0 || len(s) > 7 || (len(s) > 1 && s[0] == '0') {
		return 0, false
	}
	for _, ch := range s {
		if ch < '0' || ch > '9' {
			return 0, false
		}
	}
	n, err := strconv.Atoi(s)
	return n, err == nil
}

func expectedCodes(s string) ([]int, bool) {
	if len(s) == 0 || len(s) > 4*probe.MaxExpectedStatuses-1 {
		return nil, false
	}
	parts := strings.Split(s, ",")
	if len(parts) > probe.MaxExpectedStatuses {
		return nil, false
	}
	codes := make([]int, 0, len(parts))
	for _, part := range parts {
		n, ok := decimal(part)
		if !ok || len(part) != 3 || n < 100 || n > 599 {
			return nil, false
		}
		codes = append(codes, n)
	}
	return codes, true
}

func run(ctx context.Context, args []string, out, diagnostics io.Writer) int {
	inv, err := parse(args)
	if err != nil {
		// Only fixed parser or validation errors reach this branch.
		_, _ = fmt.Fprintln(diagnostics, "health-probe: "+err.Error())
		return 2
	}
	switch inv.mode {
	case "help":
		if _, err := io.WriteString(out, usage); err != nil {
			return outputFailure(diagnostics)
		}
		return 0
	case "demo":
		return runDemo(ctx, out, diagnostics)
	}
	report, err := probe.Run(ctx, inv.config)
	if err != nil {
		_, _ = fmt.Fprintln(diagnostics, "health-probe: invalid configuration")
		return 2
	}
	if err := json.NewEncoder(out).Encode(report); err != nil {
		return outputFailure(diagnostics)
	}
	if report.Healthy {
		return 0
	}
	return 1
}

func outputFailure(diagnostics io.Writer) int {
	_, _ = fmt.Fprintln(diagnostics, "health-probe: could not write output")
	return 1
}
