// Exercises deterministic RRF across branches, provenance retention, rejected inputs and budgets.
// These fixtures verify rank arithmetic and fail-closed boundaries; retrieval recall, graph path
// coverage, and p95/p99 on the regulatory corpus remain REQUIRED_UNMEASURED.
package retrieval

import (
	"math"
	"reflect"
	"testing"

	pb "regulagraph.local/server/gen/regulagraph/v1"
)

func candidate(kind pb.RetrieverKind, key string, rank uint32, score float64) *pb.Candidate {
	return &pb.Candidate{Retriever: kind, EvidenceKey: key, Rank: rank,
		RawScore: score, Representation: "generation:test",
		FilterDecisions: []*pb.FilterDecision{{Rule: "snapshot", Accepted: true}}}
}

func TestFuseRRFPreservesAmbiguousBranchEvidence(t *testing.T) {
	dense := pb.RetrieverKind_RETRIEVER_KIND_DENSE
	lexical := pb.RetrieverKind_RETRIEVER_KIND_BM25
	branches := []RankedBranch{
		{Kind: dense, Candidates: []*pb.Candidate{candidate(dense, "evidence:pasal-v1", 1, .98), candidate(dense, "evidence:pasal-v2", 2, .96)}},
		{Kind: lexical, Candidates: []*pb.Candidate{candidate(lexical, "evidence:pasal-v2", 1, 7.3), candidate(lexical, "evidence:pasal-v1", 2, 3.2)}},
	}
	config := RRFConfig{K: 60, Weights: map[pb.RetrieverKind]float64{dense: 1, lexical: 2},
		MaximumPerBranch: 2, MaximumTotalInputs: 4}
	first, err := FuseRRF(branches, config)
	if err != nil {
		t.Fatal(err)
	}
	second, err := FuseRRF([]RankedBranch{branches[1], branches[0]}, config)
	if err != nil || !reflect.DeepEqual(first, second) {
		t.Fatalf("branch permutation changed fusion: %v %v %v", first, second, err)
	}
	if len(first) != 2 || first[0].EvidenceKey != "evidence:pasal-v2" ||
		first[1].EvidenceKey != "evidence:pasal-v1" || len(first[0].Provenance) != 2 ||
		first[0].Provenance[0].Rank != 2 || first[0].Provenance[1].Rank != 1 {
		t.Fatalf("versioned evidence or branch ranks lost: %+v", first)
	}
	branches[0].Candidates[1].EvidenceKey = "evidence:mutated"
	if first[0].Provenance[0].EvidenceKey != "evidence:pasal-v2" {
		t.Fatal("fusion retained mutable caller-owned provenance")
	}
}

func TestFuseRRFRejectsInvalidOrUnfilteredBranches(t *testing.T) {
	dense := pb.RetrieverKind_RETRIEVER_KIND_DENSE
	base := candidate(dense, "evidence:one", 1, .2)
	config := RRFConfig{K: 60, Weights: map[pb.RetrieverKind]float64{dense: 1},
		MaximumPerBranch: 2, MaximumTotalInputs: 2}
	cases := map[string][]RankedBranch{
		"duplicate key":    {{Kind: dense, Candidates: []*pb.Candidate{base, candidate(dense, base.EvidenceKey, 2, .1)}}},
		"rank reversal":    {{Kind: dense, Candidates: []*pb.Candidate{candidate(dense, "evidence:two", 2, .1), base}}},
		"duplicate branch": {{Kind: dense, Candidates: []*pb.Candidate{base}}, {Kind: dense}},
		"over budget":      {{Kind: dense, Candidates: []*pb.Candidate{base, candidate(dense, "evidence:two", 2, .1), candidate(dense, "evidence:three", 3, .1)}}},
		"unfiltered": {{Kind: dense, Candidates: []*pb.Candidate{{Retriever: dense, EvidenceKey: "evidence:one", Rank: 1,
			Representation: "generation:test", FilterDecisions: []*pb.FilterDecision{{Rule: "snapshot", Accepted: false}}}}}},
		"missing filter decision": {{Kind: dense, Candidates: []*pb.Candidate{{Retriever: dense,
			EvidenceKey: "evidence:one", Rank: 1, Representation: "generation:test"}}}},
		"nonfinite score": {{Kind: dense, Candidates: []*pb.Candidate{candidate(dense, "evidence:one", 1, math.NaN())}}},
	}
	for name, branches := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := FuseRRF(branches, config); err == nil {
				t.Fatal("invalid fusion input accepted")
			}
		})
	}
}

func TestFuseRRFStableTieAndEmptyBranch(t *testing.T) {
	dense := pb.RetrieverKind_RETRIEVER_KIND_DENSE
	lexical := pb.RetrieverKind_RETRIEVER_KIND_BM25
	result, err := FuseRRF([]RankedBranch{
		{Kind: dense, Candidates: []*pb.Candidate{candidate(dense, "evidence:b", 1, .9)}},
		{Kind: lexical, Candidates: []*pb.Candidate{candidate(lexical, "evidence:a", 1, 5)}},
	}, RRFConfig{K: 1, Weights: map[pb.RetrieverKind]float64{dense: 1, lexical: 1},
		MaximumPerBranch: 1, MaximumTotalInputs: 2})
	if err != nil || len(result) != 2 || result[0].EvidenceKey != "evidence:a" {
		t.Fatalf("stable evidence-key tie failed: %+v %v", result, err)
	}
	result, err = FuseRRF([]RankedBranch{{Kind: dense, Candidates: []*pb.Candidate{
		candidate(dense, "evidence:b", 1, .2), candidate(dense, "evidence:a", 1, .1),
	}}}, RRFConfig{K: 1, Weights: map[pb.RetrieverKind]float64{dense: 1},
		MaximumPerBranch: 3, MaximumTotalInputs: 3})
	if err == nil {
		t.Fatalf("duplicate branch rank accepted: %+v", result)
	}
	result, err = FuseRRF([]RankedBranch{{Kind: dense}}, RRFConfig{K: 1,
		Weights: map[pb.RetrieverKind]float64{dense: 1}, MaximumPerBranch: 1, MaximumTotalInputs: 1})
	if err != nil || len(result) != 0 {
		t.Fatalf("empty branch should produce empty result: %+v %v", result, err)
	}
}
