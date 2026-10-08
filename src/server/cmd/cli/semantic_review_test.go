// Tests explicit acceptance pins, rejection of actor injection, and redacted CLI failures.
// OS identity is exercised locally; no real corpus is approved. Integration behavior belongs
// to workflow PostgreSQL tests, and fixture results do not establish benchmark acceptance.
package main

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
)

func TestSemanticReviewCLIRequiresExplicitAcceptance(t *testing.T) {
	for _, args := range [][]string{{}, {"-job", "job:test", "-action", "accept"}, {"-job", "job:test", "-actor", "spoof"}, {"-job", "job:test", "-reason", "not inspection"}, {"-job", "job:test", "-timeout", "0s"}} {
		var out, errOut bytes.Buffer
		code := runSemanticReviewWith(context.Background(), args, &out, &errOut, func(context.Context, reviewOptions) (any, error) {
			t.Fatal("invalid CLI reached execution")
			return nil, nil
		})
		if code != 2 {
			t.Fatal(args, code)
		}
	}
	var out, errOut bytes.Buffer
	args := []string{"-job", "job:test", "-action", "accept", "-expected-revision", "3", "-output-sha256", strings.Repeat("a", 64), "-reason", "reviewed"}
	if code := runSemanticReviewWith(context.Background(), args, &out, &errOut, func(ctx context.Context, o reviewOptions) (any, error) {
		if _, ok := ctx.Deadline(); !ok || o.Revision != 3 || o.Reason != "reviewed" {
			t.Fatal(o)
		}
		return map[string]string{"status": "accepted_for_execution"}, nil
	}); code != 0 || !strings.Contains(out.String(), "accepted_for_execution") {
		t.Fatal(code, out.String(), errOut.String())
	}
}

func TestSemanticReviewCLIRedactsBackendErrors(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := runSemanticReviewWith(context.Background(), []string{"-job", "job:test"}, &out, &errOut, func(context.Context, reviewOptions) (any, error) {
		return nil, errors.New("postgres://secret:password@host")
	}); code != 1 || strings.Contains(errOut.String(), "password") || out.Len() != 0 {
		t.Fatal(code, errOut.String())
	}
}

func TestSemanticReviewPrincipalIgnoresUsernameEnvironment(t *testing.T) {
	actor, err := localReviewActor()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("USERNAME", "spoof")
	t.Setenv("USER", "spoof")
	other, err := localReviewActor()
	if err != nil || other != actor || !strings.HasPrefix(actor, "operator:os:") {
		t.Fatal(actor, other, err)
	}
}
