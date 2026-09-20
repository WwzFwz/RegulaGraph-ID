// Exercises the shared ontology compiler and the typed graph gate with adversarial records.
// These tests protect cross-runtime vocabulary semantics; they do not claim extraction quality.
package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"strings"
	"testing"

	pb "regulagraph.local/server/gen/regulagraph/v1"
)

func loadRepositoryOntology(t *testing.T) *Ontology {
	t.Helper()
	raw, err := os.ReadFile("../../../../configs/ontology-v1.jsonc")
	if err != nil {
		t.Fatal(err)
	}
	ontology, err := ParseOntologyJSONC(raw)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(raw)
	if ontology.ContentHash().Sha256 != hex.EncodeToString(digest[:]) {
		t.Fatal("ontology did not retain its exact source hash")
	}
	return ontology
}

func validOntologyRecords() ([]*pb.Mention, []*pb.RelationAssertion) {
	mentions := []*pb.Mention{
		{Meta: &pb.RecordMeta{RecordId: "mention:organization"}, CandidateType: "organization"},
		{Meta: &pb.RecordMeta{RecordId: "mention:permit"}, CandidateType: "permit"},
	}
	assertions := []*pb.RelationAssertion{{
		Meta: &pb.RecordMeta{RecordId: "assertion:requires"}, SubjectId: "mention:organization",
		PredicateId: "requires", ObjectId: "mention:permit", Origin: pb.AssertionOrigin_ASSERTION_ORIGIN_EXPLICIT,
		OntologyVersion: "id-regulation-ontology-v1",
		Qualifiers: []*pb.Qualifier{{PredicateId: "scope", Value: &pb.Qualifier_MentionId{MentionId: "mention:permit"}}},
	}}
	return mentions, assertions
}

func TestOntologyAcceptsTypedExtractionRecords(t *testing.T) {
	ontology := loadRepositoryOntology(t)
	mentions, assertions := validOntologyRecords()
	if err := ontology.ValidateExtractionRecords(ontology.Version(), mentions, assertions); err != nil {
		t.Fatal(err)
	}
}

func TestOntologyRejectsInvalidGraphSemantics(t *testing.T) {
	ontology := loadRepositoryOntology(t)
	tests := map[string]func([]*pb.Mention, []*pb.RelationAssertion){
		"unknown mention type": func(mentions []*pb.Mention, _ []*pb.RelationAssertion) { mentions[0].CandidateType = "agency_typo" },
		"unknown predicate": func(_ []*pb.Mention, assertions []*pb.RelationAssertion) { assertions[0].PredicateId = "requires_typo" },
		"invalid endpoint type": func(mentions []*pb.Mention, _ []*pb.RelationAssertion) { mentions[0].CandidateType = "date" },
		"self edge": func(_ []*pb.Mention, assertions []*pb.RelationAssertion) { assertions[0].ObjectId = assertions[0].SubjectId },
		"wrong qualifier kind": func(_ []*pb.Mention, assertions []*pb.RelationAssertion) {
			assertions[0].Qualifiers[0] = &pb.Qualifier{PredicateId: "effective_on", Value: &pb.Qualifier_Literal{Literal: "segera"}}
		},
		"disallowed origin": func(_ []*pb.Mention, assertions []*pb.RelationAssertion) {
			assertions[0].PredicateId = "defines"
			assertions[0].Origin = pb.AssertionOrigin_ASSERTION_ORIGIN_INFERRED
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			mentions, assertions := validOntologyRecords()
			mutate(mentions, assertions)
			if err := ontology.ValidateExtractionRecords(ontology.Version(), mentions, assertions); err == nil {
				t.Fatal("invalid graph semantics were accepted")
			}
		})
	}
}

func TestOntologyCompilerRejectsAmbiguousConfiguration(t *testing.T) {
	valid := `{"schema_version":1,"ontology_version":"v1","entity_types":["thing"],"qualifiers":[{"id":"scope","value_kinds":["literal"]}],"predicates":[{"id":"links","subject_types":["thing"],"object_types":["thing"],"qualifier_ids":["scope"],"origins":["explicit"],"allow_self":false}]}`
	tests := map[string]string{
		"unknown field": strings.Replace(valid, `"schema_version":1`, `"schema_version":1,"surprise":true`, 1),
		"duplicate entity": strings.Replace(valid, `["thing"]`, `["thing","thing"]`, 1),
		"dangling endpoint": strings.Replace(valid, `"subject_types":["thing"]`, `"subject_types":["missing"]`, 1),
		"multiple documents": valid + `{}`,
	}
	for name, raw := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseOntologyJSONC([]byte(raw)); err == nil {
				t.Fatal("invalid ontology configuration was accepted")
			}
		})
	}
}
