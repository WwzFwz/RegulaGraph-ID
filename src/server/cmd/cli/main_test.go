// CLI validation tests: invalid invocation fails before network or filesystem acquisition.
// Role: keep collector limits and exit codes explicit; live PDF checks are separate smoke runs.
package main

import (
	"bytes"
	"context"
	"testing"
)

func TestCLIValidation(t *testing.T) {
	cases := []struct {
		args []string
		code int
	}{
		{nil, 2}, {[]string{"collect", "-help"}, 0}, {[]string{"collect"}, 2},
		{[]string{"discover", "-help"}, 0}, {[]string{"discover"}, 2},
		{[]string{"audit", "-help"}, 0},
		{[]string{"collect", "-workers", "0"}, 2}, {[]string{"collect", "-max-pdf-mib", "0"}, 2},
		{[]string{"collect", "unexpected"}, 2}, {[]string{"collect", "-interval", "-1s"}, 2},
	}
	for _, tt := range cases {
		var out, errs bytes.Buffer
		got := run(context.Background(), tt.args, &out, &errs)
		if got != tt.code {
			t.Fatalf("args %v: code %d want %d: %s", tt.args, got, tt.code, errs.String())
		}
	}
}
