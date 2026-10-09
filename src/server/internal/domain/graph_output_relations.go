// Checks the Rust canonical hash-v1 relation projection at the Go return boundary.
// Directed endpoints, qualifiers, exception DAG and provenance are never inferred:
// only committed mention assignments and source records are projected. Protobuf
// canonical bytes/hash domains follow assembly/canonical.rs and need cross-language
// fixtures on changes. Iterative DAG traversal avoids recursion on deep exceptions;
// cost is O(records+edges) plus bounded message/set sorting and hashing.
package domain

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"sort"

	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
)

func graphCanonicalID(prefix string, m proto.Message) (string, error) {
	raw, err := proto.MarshalOptions{Deterministic: true}.Marshal(m)
	if err != nil {
		return "", err
	}
	h := sha256.New()
	h.Write([]byte(prefix))
	h.Write([]byte{0})
	h.Write(raw)
	return fmt.Sprintf("%s%x", prefix, h.Sum(nil)), nil
}

// Messages have been bounded and rejected for unknown fields/maps at entry.
func graphSortedUnique[T proto.Message](values []T) ([]T, error) {
	type encoded struct {
		raw   []byte
		value T
	}
	items := make([]encoded, 0, len(values))
	for _, value := range values {
		raw, err := proto.MarshalOptions{Deterministic: true}.Marshal(value)
		if err != nil {
			return nil, err
		}
		items = append(items, encoded{raw, value})
	}
	sort.Slice(items, func(i, j int) bool { return bytes.Compare(items[i].raw, items[j].raw) < 0 })
	out := make([]T, 0, len(items))
	for i, item := range items {
		if i == 0 || !bytes.Equal(items[i-1].raw, item.raw) {
			out = append(out, item.value)
		}
	}
	return out, nil
}

func graphSortedStrings(values []string) []string {
	sort.Strings(values)
	out := values[:0]
	for _, value := range values {
		if len(out) == 0 || out[len(out)-1] != value {
			out = append(out, value)
		}
	}
	return out
}

func expectedGraphRelations(e *pb.ExtractionBatch, assigned map[string]string, ontology *Ontology) ([]*pb.RelationAssertion, []*pb.SupportRecord, error) {
	byID := map[string]*pb.RelationAssertion{}
	parents := map[string][]string{}
	pending := map[string]int{}
	var ready []string
	for _, a := range e.Assertions {
		byID[a.Meta.RecordId] = a
	}
	for _, a := range e.Assertions {
		exceptions := graphSortedStrings(append([]string(nil), a.ExceptionRefs...))
		for _, id := range exceptions {
			if byID[id] == nil {
				return nil, nil, errors.New("missing graph exception")
			}
			parents[id] = append(parents[id], a.Meta.RecordId)
		}
		pending[a.Meta.RecordId] = len(exceptions)
		if len(exceptions) == 0 {
			ready = append(ready, a.Meta.RecordId)
		}
	}
	mapped, assertions := map[string]string{}, map[string]*pb.RelationAssertion{}
	for i := 0; i < len(ready); i++ {
		id := ready[i]
		a := proto.Clone(byID[id]).(*pb.RelationAssertion)
		a.Meta.RecordId = ""
		a.SubjectId, a.ObjectId = assigned[a.SubjectId], assigned[a.ObjectId]
		if a.SubjectId == "" || a.ObjectId == "" || !ontology.predicates[a.PredicateId].allowSelf && a.SubjectId == a.ObjectId {
			return nil, nil, errors.New("invalid canonical assertion endpoints")
		}
		for _, q := range a.Qualifiers {
			switch value := q.Value.(type) {
			case *pb.Qualifier_MentionId:
				canonical := assigned[value.MentionId]
				if canonical == "" {
					return nil, nil, errors.New("unresolved qualifier mention")
				}
				q.Value = &pb.Qualifier_CanonicalId{CanonicalId: canonical}
			case *pb.Qualifier_CanonicalId:
				return nil, nil, errors.New("pre-resolved extraction qualifier")
			case *pb.Qualifier_Number:
				if value.Number == 0 {
					value.Number = 0
				}
			}
		}
		for n, old := range a.ExceptionRefs {
			a.ExceptionRefs[n] = mapped[old]
		}
		a.ExceptionRefs = graphSortedStrings(a.ExceptionRefs)
		var err error
		a.Qualifiers, err = graphSortedUnique(a.Qualifiers)
		if err != nil {
			return nil, nil, err
		}
		a.TemporalScope.CompareDates, err = graphSortedUnique(a.TemporalScope.CompareDates)
		if err != nil {
			return nil, nil, err
		}
		key, err := graphCanonicalID("assertion:canonical:v1:", a)
		if err != nil {
			return nil, nil, err
		}
		a.Meta.RecordId = key
		if previous := assertions[key]; previous != nil && !proto.Equal(previous, a) {
			return nil, nil, errors.New("canonical assertion hash collision")
		}
		assertions[key] = a
		mapped[id] = key
		for _, parent := range parents[id] {
			pending[parent]--
			if pending[parent] == 0 {
				ready = append(ready, parent)
			}
		}
	}
	if len(mapped) != len(e.Assertions) {
		return nil, nil, errors.New("cyclic graph exceptions")
	}
	supports, covered := map[string]*pb.SupportRecord{}, map[string]bool{}
	for _, original := range e.Supports {
		s := proto.Clone(original).(*pb.SupportRecord)
		s.Meta.RecordId = ""
		s.AssertionId = mapped[original.AssertionId]
		if s.AssertionId == "" {
			return nil, nil, errors.New("support has no source assertion")
		}
		var err error
		s.SourceRefs, err = graphSortedUnique(s.SourceRefs)
		if err != nil {
			return nil, nil, err
		}
		s.EvidenceSpans, err = graphSortedUnique(s.EvidenceSpans)
		if err != nil {
			return nil, nil, err
		}
		key, err := graphCanonicalID("support:canonical:v1:", s)
		if err != nil {
			return nil, nil, err
		}
		s.Meta.RecordId = key
		if previous := supports[key]; previous != nil && !proto.Equal(previous, s) {
			return nil, nil, errors.New("canonical support hash collision")
		}
		supports[key] = s
		covered[s.AssertionId] = true
	}
	if len(covered) != len(assertions) {
		return nil, nil, errors.New("graph assertion without source support")
	}
	var as []*pb.RelationAssertion
	var ss []*pb.SupportRecord
	for _, a := range assertions {
		as = append(as, a)
	}
	for _, s := range supports {
		ss = append(ss, s)
	}
	sort.Slice(as, func(i, j int) bool { return as[i].Meta.RecordId < as[j].Meta.RecordId })
	sort.Slice(ss, func(i, j int) bool { return ss[i].Meta.RecordId < ss[j].Meta.RecordId })
	return as, ss, nil
}
