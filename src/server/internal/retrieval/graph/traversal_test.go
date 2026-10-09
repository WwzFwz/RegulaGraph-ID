// Exercises production traversal against a deterministic batch-reader fixture:
// alternate paths, cycles, reverse discovery without predicate reversal, explicit
// resource stops, reader corruption and cancellation. This is algorithm evidence,
// not source/model quality; the Neo4j native fixture tests physical integration.
package graph

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/domain"
)

type fixtureReader struct {
	snapshot   *pb.SnapshotRef
	entities   map[string]*pb.CanonicalEntity
	assertions []*pb.RelationAssertion
	supports   []*pb.SupportRecord
	mutate     func(*domain.GraphNeighborhood)
	err        error
}

func (f *fixtureReader) ReadNeighborhood(ctx context.Context, seeds []string, limits domain.GraphReadLimits) (*domain.GraphNeighborhood, error) {
	if f.err != nil {
		return nil, f.err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	b := &domain.GraphNeighborhood{Snapshot: proto.Clone(f.snapshot).(*pb.SnapshotRef)}
	selected := map[string]bool{}
	nodes := map[string]bool{}
	for _, id := range seeds {
		selected[id] = true
		nodes[id] = true
	}
	for _, a := range f.assertions {
		if !selected[a.SubjectId] && !selected[a.ObjectId] {
			continue
		}
		if len(b.Assertions) == limits.Assertions {
			b.MoreAssertions = true
			break
		}
		b.Assertions = append(b.Assertions, proto.Clone(a).(*pb.RelationAssertion))
		nodes[a.SubjectId] = true
		nodes[a.ObjectId] = true
		for _, s := range f.supports {
			if s.AssertionId == a.Meta.RecordId {
				b.Supports = append(b.Supports, proto.Clone(s).(*pb.SupportRecord))
			}
		}
	}
	for id := range nodes {
		b.Entities = append(b.Entities, proto.Clone(f.entities[id]).(*pb.CanonicalEntity))
	}
	sort.Slice(b.Entities, func(i, j int) bool { return b.Entities[i].Meta.RecordId < b.Entities[j].Meta.RecordId })
	recount(b)
	if len(b.Supports) > limits.Supports || b.ReadBytes > limits.Bytes {
		return nil, domain.ErrGraphReadBudget
	}
	if f.mutate != nil {
		f.mutate(b)
	}
	return b, nil
}

func recount(b *domain.GraphNeighborhood) {
	b.ReadBytes = 0
	for _, v := range b.Entities {
		b.ReadBytes += uint64(proto.Size(v))
	}
	for _, v := range b.Assertions {
		b.ReadBytes += uint64(proto.Size(v))
	}
	for _, v := range b.Supports {
		b.ReadBytes += uint64(proto.Size(v))
	}
}

func traversalFixture() (*fixtureReader, TraversalConfig) {
	hash := &pb.ContentHash{Sha256: strings.Repeat("a", 64)}
	f := &fixtureReader{snapshot: &pb.SnapshotRef{CorpusId: "corpus:graph", SnapshotId: "snapshot:graph", Sequence: 2, ManifestHash: hash, RepresentationGeneration: "generation:index"}, entities: map[string]*pb.CanonicalEntity{}}
	meta := func(id string) *pb.RecordMeta {
		return &pb.RecordMeta{SchemaVersion: 1, CorpusId: f.snapshot.CorpusId, RecordId: id, Visibility: &pb.Visibility{FromSeq: 2}}
	}
	for _, id := range []string{"a", "b", "c", "d"} {
		f.entities[id] = &pb.CanonicalEntity{Meta: meta(id), EntityType: "organization", PreferredLabel: id, Scope: "ID", RegistryRevision: 1, ReviewState: pb.ReviewState_REVIEW_STATE_UNREVIEWED}
	}
	for _, edge := range [][3]string{{"ab", "a", "b"}, {"bd", "b", "d"}, {"ca", "c", "a"}, {"dc", "d", "c"}} {
		f.assertions = append(f.assertions, &pb.RelationAssertion{Meta: meta(edge[0]), SubjectId: edge[1], ObjectId: edge[2], PredicateId: "references", OntologyVersion: "v1", Origin: pb.AssertionOrigin_ASSERTION_ORIGIN_EXPLICIT, TemporalScope: &pb.TemporalScope{Mode: pb.TemporalMode_TEMPORAL_MODE_CURRENT, UnresolvedPolicy: pb.UnresolvedPolicy_UNRESOLVED_POLICY_REPORT}})
		f.supports = append(f.supports, &pb.SupportRecord{Meta: meta("support:" + edge[0]), AssertionId: edge[0], IndependentSourceGroup: "source:one", ReviewState: pb.ReviewState_REVIEW_STATE_UNREVIEWED, ExtractionManifest: &pb.ProducerManifest{Software: "fixture", Build: "1", SchemaVersion: 1, ConfigHash: hash}, EvidenceSpans: []*pb.TextSpan{{TextArtifactId: "text:one", StartByte: 0, EndByte: 5}}, SourceRefs: []*pb.SourceVersionRef{{SourceBlobId: "blob:one", RegulationId: "reg:one", ProvisionVersionId: "version:one"}}})
	}
	second := proto.Clone(f.supports[0]).(*pb.SupportRecord)
	second.Meta.RecordId = "support:ab2"
	f.supports = append(f.supports, second)
	return f, TraversalConfig{MaximumHops: 4, MaximumPaths: 100, Read: domain.GraphReadLimits{Assertions: 128, Supports: 256, Bytes: 1 << 20}}
}

func TestTraversalPreservesAlternateSourcedPaths(t *testing.T) {
	f, c := traversalFixture()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	got, err := Traverse(ctx, f, f.snapshot, []string{"a"}, c)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Paths) != 6 || !got.FrontierExhausted || len(got.StopReasons) != 0 || got.ReadBatches != 4 {
		t.Fatalf("unexpected accounting: paths=%d exhausted=%v batches=%d reasons=%v", len(got.Paths), got.FrontierExhausted, got.ReadBatches, got.StopReasons)
	}
	paths := map[string]*pb.GraphPath{}
	for _, p := range got.Paths {
		if err := domain.ValidateWire(p, domain.DefaultWireLimits); err != nil {
			t.Fatal(err)
		}
		paths[strings.Join(p.OrderedNodeIds, ",")] = p
		if p.Coverage != pb.Completeness_COMPLETENESS_PARTIAL {
			t.Fatal("discovery claimed source/temporal coverage")
		}
		seen := map[string]bool{}
		for _, id := range p.OrderedNodeIds {
			if seen[id] {
				t.Fatal("cycle returned")
			}
			seen[id] = true
		}
	}
	if paths["a,b,d"] == nil || paths["a,c,d"] == nil || paths["a,b"].SelectedSupportIds[0] != "support:ab" || got.Supports["support:ab2"] == nil {
		t.Fatal("alternate path/shared support lost")
	}
	if got.Assertions["ca"].SubjectId != "c" || paths["a,c"].OrderedAssertionIds[0] != "ca" {
		t.Fatal("reverse discovery changed predicate direction")
	}
	again, err := Traverse(ctx, f, f.snapshot, []string{"a"}, c)
	if err != nil {
		t.Fatal(err)
	}
	for i, p := range got.Paths {
		if !proto.Equal(p, again.Paths[i]) {
			t.Fatal("nondeterministic paths")
		}
	}
}

func TestTraversalReportsResourceStops(t *testing.T) {
	for _, reason := range []string{"hop_budget", "path_budget", "assertion_budget", "read_budget"} {
		t.Run(reason, func(t *testing.T) {
			f, c := traversalFixture()
			switch reason {
			case "hop_budget":
				c.MaximumHops = 1
			case "path_budget":
				c.MaximumPaths = 1
			case "assertion_budget":
				c.Read.Assertions = 1
			case "read_budget":
				c.Read.Supports = 1
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			got, err := Traverse(ctx, f, f.snapshot, []string{"a"}, c)
			if err != nil {
				t.Fatal(err)
			}
			if got.FrontierExhausted {
				t.Fatal("truncated traversal claimed exhaustion")
			}
			found := false
			for _, r := range got.StopReasons {
				found = found || r == reason
			}
			if !found {
				t.Fatal(got.StopReasons)
			}
		})
	}
}

func TestTraversalRejectsCorruptNeighborhood(t *testing.T) {
	for name, mutate := range map[string]func(*domain.GraphNeighborhood){
		"snapshot":         func(b *domain.GraphNeighborhood) { b.Snapshot.Sequence++ },
		"support":          func(b *domain.GraphNeighborhood) { b.Supports = nil; recount(b) },
		"foreign endpoint": func(b *domain.GraphNeighborhood) { b.Assertions[0].ObjectId = "absent"; recount(b) },
		"bytes":            func(b *domain.GraphNeighborhood) { b.ReadBytes-- },
		"closed":           func(b *domain.GraphNeighborhood) { b.Assertions[0].Meta.Visibility.ToSeq = proto.Uint64(3); recount(b) },
		"missing seed":     func(b *domain.GraphNeighborhood) { b.Entities = b.Entities[1:]; recount(b) },
	} {
		t.Run(name, func(t *testing.T) {
			f, c := traversalFixture()
			f.mutate = mutate
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			if got, err := Traverse(ctx, f, f.snapshot, []string{"a"}, c); err == nil || got != nil {
				t.Fatal("corrupt batch admitted")
			}
		})
	}
	f, c := traversalFixture()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	cancel()
	if got, err := Traverse(ctx, f, f.snapshot, []string{"a"}, c); !errors.Is(err, context.Canceled) || got != nil {
		t.Fatal("cancelled traversal returned paths")
	}
	ctx2, cancel2 := context.WithTimeout(context.Background(), time.Second)
	defer cancel2()
	f.err = errors.New("backend failed")
	if got, err := Traverse(ctx2, f, f.snapshot, []string{"a"}, c); err == nil || got != nil {
		t.Fatal("backend failure hidden")
	}
}

type reusingReader struct {
	delegate *fixtureReader
	previous *domain.GraphNeighborhood
	calls    int
}

func (r *reusingReader) ReadNeighborhood(ctx context.Context, seeds []string, limits domain.GraphReadLimits) (*domain.GraphNeighborhood, error) {
	r.calls++
	if r.calls == 3 {
		for _, a := range r.previous.Assertions {
			a.SubjectId = "changed"
			a.ObjectId = "changed"
			a.Meta.RecordId = "changed"
		}
	}
	b, err := r.delegate.ReadNeighborhood(ctx, seeds, limits)
	r.previous = b
	return b, err
}

func TestTraversalOwnsRecordsAcrossFrontierBatches(t *testing.T) {
	f, c := traversalFixture()
	entity := proto.Clone(f.entities["b"]).(*pb.CanonicalEntity)
	assertion := proto.Clone(f.assertions[0]).(*pb.RelationAssertion)
	support := proto.Clone(f.supports[0]).(*pb.SupportRecord)
	f.entities = map[string]*pb.CanonicalEntity{"a": f.entities["a"]}
	f.assertions = nil
	f.supports = nil
	for i := 0; i < 65; i++ {
		id := fmt.Sprintf("leaf:%03d", i)
		e := proto.Clone(entity).(*pb.CanonicalEntity)
		e.Meta.RecordId = id
		f.entities[id] = e
		a := proto.Clone(assertion).(*pb.RelationAssertion)
		a.Meta.RecordId = "assertion:" + id
		a.ObjectId = id
		f.assertions = append(f.assertions, a)
		s := proto.Clone(support).(*pb.SupportRecord)
		s.Meta.RecordId = "support:" + id
		s.AssertionId = a.Meta.RecordId
		f.supports = append(f.supports, s)
	}
	c.MaximumPaths = 1000
	c.MaximumHops = 2
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	got, err := Traverse(ctx, &reusingReader{delegate: f}, f.snapshot, []string{"a"}, c)
	if err != nil || len(got.Paths) != 65 || !got.FrontierExhausted {
		t.Fatal("reader reused admitted buffers", got, err)
	}
}
