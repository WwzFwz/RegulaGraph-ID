// Checks explicit operator acceptance pins, deadline propagation and redacted
// failures for review-alias. These command tests do not replace the PostgreSQL
// source/CAS/replay integration tests or establish semantic mapping quality.
package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"
)

func TestSourcedAliasCommand(t *testing.T) {
	base := []string{"-source", "artifact:extract", "-target-document", "artifact:document", "-mention", "mention:one", "-canonical", "canonical:one", "-scope", "ID:national", "-label", "Name"}
	for _, name := range []string{"inspect", "accept", "accept-without-pins", "inspect-with-approval", "invalid-timeout", "backend-error", "output-error"} {
		t.Run(name, func(t *testing.T) {
			args := append([]string(nil), base...)
			if name == "accept" {
				args = append(args, "-action", "accept", "-expected-revision", "3", "-operation", "alias:one", "-plan-sha256", strings.Repeat("a", 64), "-reason", "reviewed source and target")
			}
			if name == "accept-without-pins" {
				args = append(args, "-action", "accept")
			}
			if name == "inspect-with-approval" {
				args = append(args, "-reason", "not an inspection")
			}
			if name == "invalid-timeout" {
				args = append(args, "-timeout", "0s")
			}
			var out, errOut bytes.Buffer
			var writer io.Writer = &out
			if name == "output-error" {
				writer = publicationBrokenWriter{}
			}
			calls := 0
			code := runSourcedAliasWith(context.Background(), args, writer, &errOut, func(ctx context.Context, o sourcedAliasOptions) (any, error) {
				calls++
				if _, ok := ctx.Deadline(); !ok {
					t.Fatal("missing deadline")
				}
				if o.Canonical != "canonical:one" || o.TargetDocument != "artifact:document" {
					t.Fatal("lost explicit target")
				}
				if name == "backend-error" {
					return nil, errors.New("secret DSN")
				}
				return map[string]any{"status": o.Action}, nil
			})
			want := 0
			if name == "backend-error" || name == "output-error" {
				want = 1
			}
			if name == "accept-without-pins" || name == "inspect-with-approval" || name == "invalid-timeout" {
				want = 2
			}
			if code != want || strings.Contains(errOut.String(), "secret") {
				t.Fatal(code, errOut.String())
			}
			if want == 2 && calls != 0 {
				t.Fatal("invalid arguments reached storage")
			}
		})
	}
}

func TestProvisionalAliasCommandSelection(t *testing.T) {
	base := []string{"-create-provisional", "-source", "artifact:extract", "-mention", "mention:one", "-scope", "ID:national", "-label", "term"}
	for _, mode := range []string{"inspect", "accept", "missing-approval", "canonical", "target"} {
		t.Run(mode, func(t *testing.T) {
			args := append([]string(nil), base...)
			switch mode {
			case "accept":
				args = append(args, "-action", "accept", "-expected-revision", "5", "-operation", "provisional:test", "-plan-sha256", strings.Repeat("a", 64), "-reason", "reviewed provisional occurrence")
			case "missing-approval":
				args = append(args, "-action", "accept")
			case "canonical":
				args = append(args, "-canonical", "canonical:provided")
			case "target":
				args = append(args, "-target-document", "artifact:target")
			}
			var out, errOut bytes.Buffer
			calls := 0
			code := runSourcedAliasWith(context.Background(), args, &out, &errOut, func(ctx context.Context, o sourcedAliasOptions) (any, error) {
				calls++
				if !o.CreateProvisional || o.Canonical != "" || o.TargetDocument != "" {
					t.Fatal("lost creation mode")
				}
				return map[string]any{"create_provisional": true}, nil
			})
			valid := mode == "inspect" || mode == "accept"
			if valid {
				if code != 0 || calls != 1 {
					t.Fatal(code, calls, errOut.String())
				}
			} else if code != 2 || calls != 0 {
				t.Fatal("invalid selection reached workflow", code, calls)
			}
		})
	}
}
