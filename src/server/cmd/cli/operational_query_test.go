// Opt-in smoke of an existing operator-owned published corpus through the real
// query entry point. Does not mutate corpus data; the workflow acquires/releases
// normal read leases. Requires operational query environment plus an explicit
// question/date. Optional REGULAGRAPH_TEST_OPERATIONAL_ANSWER=1 additionally
// exercises the pinned generator and existing answer/citation output checks.
// This proves wiring, not gold relevance or latency acceptance.
package main

import (
	"bytes"
	"context"
	"os"
	"testing"

	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/workflows"
)

func TestOperationalPublishedQuery(t *testing.T) {
	question := os.Getenv("REGULAGRAPH_TEST_OPERATIONAL_QUESTION")
	if question == "" {
		t.Skip("operational corpus not selected")
	}
	var out, errOut bytes.Buffer
	args := []string{"-question", question, "-as-of", os.Getenv("REGULAGRAPH_TEST_OPERATIONAL_DATE"), "-profile", "hybrid", "-unresolved", "report", "-timeout", "5m"}
	if os.Getenv("REGULAGRAPH_TEST_OPERATIONAL_ANSWER") == "1" {
		args = append(args, "-answer")
	}
	code := runQueryEvidenceWith(context.Background(), args, &out, &errOut, os.Getenv, func(ctx context.Context, opts queryOptions, request *pb.QuestionRequest) (*workflows.RAGResult, error) {
		result, err := executeEvidenceQuery(ctx, opts, request)
		if err == nil && (result == nil || result.Evidence == nil || len(result.Evidence.Items) == 0) {
			t.Fatal("selected smoke question must retrieve source evidence")
		}
		return result, err
	})
	if code != 0 {
		t.Fatalf("query exit %d: %s", code, errOut.String())
	}
	if out.Len() == 0 {
		t.Fatal("missing evidence output")
	}
}
