// Exercises backend projection and real Neo4j transactions with synthetic C01
// graph records. Tests cover shared support, replay, immutable conflicts, atomic
// rollback, sealing, readback corruption and namespace isolation. They do not
// prove source/model quality or PostgreSQL publication. Real tests require the
// explicit disposable Bolt URI/password; skipped tests are not backend PASS.
package neo4j

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	bolt "github.com/neo4j/neo4j-go-driver/v5/neo4j"
	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
)

func graphFixture(corpus string) (Binding, *pb.GraphDelta) {
	hash := &pb.ContentHash{Sha256: strings.Repeat("a", 64)}
	base := &pb.SnapshotRef{CorpusId: corpus, SnapshotId: "snapshot:base", Sequence: 1, ManifestHash: hash, RepresentationGeneration: "generation:index"}
	b := Binding{CorpusID: corpus, Generation: "generation:graph", PublicationID: "publication:graph", Fence: 1, Sequence: 2, RegistryRevision: 3, BaseSnapshot: base}
	meta := func(id string) *pb.RecordMeta {
		return &pb.RecordMeta{SchemaVersion: 1, CorpusId: corpus, RecordId: id, Visibility: &pb.Visibility{FromSeq: 2}}
	}
	producer := &pb.ProducerManifest{Software: "fixture", Build: "1", SchemaVersion: 1, ConfigHash: hash}
	d := &pb.GraphDelta{Meta: &pb.RecordMeta{SchemaVersion: 1, CorpusId: corpus, RecordId: "delta:first"}, BaseSnapshot: base, RegistryRevision: 3, OntologyVersion: "fixture-v1",
		Dependencies: &pb.DependencyManifest{ArtifactId: "dependencies:first", ProducerManifest: producer}, ValidationReport: &pb.ValidationReport{Valid: true, CheckedRecords: 5},
		Entities: []*pb.CanonicalEntity{{Meta: meta("entity:a"), EntityType: "organization", PreferredLabel: "A", Scope: "ID", RegistryRevision: 3, ReviewState: pb.ReviewState_REVIEW_STATE_UNREVIEWED}, {Meta: meta("entity:b"), EntityType: "organization", PreferredLabel: "B", Scope: "ID", RegistryRevision: 3, ReviewState: pb.ReviewState_REVIEW_STATE_UNREVIEWED}},
		Assertions: []*pb.RelationAssertion{{Meta: meta("assertion:ab"), SubjectId: "entity:a", ObjectId: "entity:b", PredicateId: "references", OntologyVersion: "fixture-v1", Origin: pb.AssertionOrigin_ASSERTION_ORIGIN_EXPLICIT,
			TemporalScope: &pb.TemporalScope{Mode: pb.TemporalMode_TEMPORAL_MODE_CURRENT, UnresolvedPolicy: pb.UnresolvedPolicy_UNRESOLVED_POLICY_REPORT}}},
		Supports: []*pb.SupportRecord{{Meta: meta("support:first"), AssertionId: "assertion:ab", IndependentSourceGroup: "source-group:first", ReviewState: pb.ReviewState_REVIEW_STATE_UNREVIEWED, ExtractionManifest: producer,
			EvidenceSpans: []*pb.TextSpan{{TextArtifactId: "text:first", StartByte: 0, EndByte: 4}}, SourceRefs: []*pb.SourceVersionRef{{SourceBlobId: "blob:first", RegulationId: "regulation:first", ProvisionVersionId: "version:first"}}}},
	}
	return b, d
}

func TestProjectionRejectsInvalidGraph(t *testing.T) {
	b, d := graphFixture("corpus:projection")
	s := &Store{binding: b}
	if p, err := s.project(d); err != nil || len(p.records) != 4 || len(p.edges) != 3 {
		t.Fatal("valid fixture", err)
	}
	for name, mutate := range map[string]func(*pb.GraphDelta){
		"missing endpoint":              func(d *pb.GraphDelta) { d.Entities = d.Entities[:1] },
		"missing support":               func(d *pb.GraphDelta) { d.Supports = nil },
		"wrong assertion endpoint kind": func(d *pb.GraphDelta) { d.Assertions[0].SubjectId = d.Supports[0].Meta.RecordId },
		"duplicate identity":            func(d *pb.GraphDelta) { d.Entities[1].Meta.RecordId = d.Entities[0].Meta.RecordId },
		"wrong visibility":              func(d *pb.GraphDelta) { d.Supports[0].Meta.Visibility.FromSeq = 1 },
		"closed record":                 func(d *pb.GraphDelta) { d.Supports[0].Meta.Visibility.ToSeq = proto.Uint64(3) },
		"closure": func(d *pb.GraphDelta) {
			d.VisibilityClosures = []*pb.VisibilityClosure{{RecordId: "entity:a", ExpectedFromSeq: 1, ToSeq: 2}}
		},
		"false count":    func(d *pb.GraphDelta) { d.ValidationReport.CheckedRecords++ },
		"wrong base":     func(d *pb.GraphDelta) { d.BaseSnapshot.Sequence = 3 },
		"wrong revision": func(d *pb.GraphDelta) { d.RegistryRevision++ },
	} {
		t.Run(name, func(t *testing.T) {
			copy := proto.Clone(d).(*pb.GraphDelta)
			mutate(copy)
			if _, err := s.project(copy); err == nil {
				t.Fatal("invalid delta projected")
			}
		})
	}
}

func TestDriverConfigurationIsLazyAndBounded(t *testing.T) {
	b, _ := graphFixture("corpus:config")
	c := Config{URI: "bolt://127.0.0.1:1", Username: "user", Password: "secret", Database: "neo4j", PoolSize: 2, Timeout: time.Second}
	s, err := New(c, b)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close(context.Background())
	for _, uri := range []string{"bolt://remote.example:7687", "neo4j://127.0.0.1:7687", "bolt://user:secret@127.0.0.1:7687", "bolt://127.0.0.1:7687?region=x"} {
		bad := c
		bad.URI = uri
		if _, err := New(bad, b); err == nil {
			t.Fatal("unsafe or routed URI accepted")
		}
	}
	b.BaseSnapshot = nil
	if _, err := New(c, b); err == nil {
		t.Fatal("nil base admitted")
	}
}

func TestNeo4jGraphAgainstStore(t *testing.T) {
	uri := os.Getenv("REGULAGRAPH_TEST_NEO4J_URI")
	if uri == "" {
		t.Skip("disposable Neo4j URI required")
	}
	password := os.Getenv("REGULAGRAPH_TEST_NEO4J_PASSWORD")
	if password == "" {
		t.Fatal("Neo4j test password required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	b, first := graphFixture(fmt.Sprintf("corpus:neo4j:%d", time.Now().UnixNano()))
	c := Config{URI: uri, Username: "neo4j", Password: password, Database: "neo4j", PoolSize: 4, Timeout: 20 * time.Second}
	s, err := New(c, b)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close(context.Background())
	defer func() {
		cleanup, done := context.WithTimeout(context.Background(), 10*time.Second)
		defer done()
		e := s.transaction(cleanup, true, func(ctx context.Context, tx bolt.ExplicitTransaction) error {
			r, e := tx.Run(ctx, `MATCH (n) WHERE n.corpus=$corpus DETACH DELETE n`, map[string]any{"corpus": b.CorpusID})
			if e != nil {
				return e
			}
			_, e = r.Consume(ctx)
			return e
		})
		if e != nil {
			t.Error("fixture cleanup", e)
		}
	}()
	if err = s.ApplyGraphDelta(ctx, first); err == nil {
		t.Fatal("write before schema admission")
	}
	if err = s.EnsureSchema(ctx); err != nil {
		t.Fatal("schema", err)
	}
	if err = s.EnsureSchema(ctx); err != nil {
		t.Fatal("schema replay", err)
	}
	// Concurrent exact retries must create one operation and no duplicated edges.
	var wg sync.WaitGroup
	errs := make(chan error, 4)
	for range 4 {
		wg.Add(1)
		go func() { defer wg.Done(); errs <- s.ApplyGraphDelta(ctx, first) }()
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		if e != nil {
			t.Fatal("concurrent replay", e)
		}
	}
	second := proto.Clone(first).(*pb.GraphDelta)
	second.Meta.RecordId = "delta:second"
	second.Supports[0].Meta.RecordId = "support:second"
	second.Supports[0].IndependentSourceGroup = "source-group:second"
	second.Supports[0].SourceRefs[0].SourceBlobId = "blob:second"
	if err = s.ApplyGraphDelta(ctx, second); err != nil {
		t.Fatal("shared assertion second support", err)
	}
	// Fault-inject near/full intake counters to exercise the admission boundary
	// without allocating 64MiB of model-like payload in a routine integration run.
	for _, budget := range [][2]int64{{256, 0}, {2, 64 << 20}, {-1, 0}, {2, -1}} {
		if e := s.transaction(ctx, true, func(ctx context.Context, tx bolt.ExplicitTransaction) error {
			params := s.params()
			params["count"], params["bytes"] = budget[0], budget[1]
			_, e := one(ctx, tx, `MATCH (g:RGGeneration {corpus:$corpus,generation:$generation}) SET g.operations=$count,g.bytes=$bytes RETURN g.operations`, params)
			return e
		}); e != nil {
			t.Fatal(e)
		}
		newDelta := proto.Clone(first).(*pb.GraphDelta)
		newDelta.Meta.RecordId = "delta:over-budget"
		if e := s.ApplyGraphDelta(ctx, newDelta); e == nil {
			t.Fatal("generation intake budget ignored", budget)
		}
		if e := s.ApplyGraphDelta(ctx, first); e != nil {
			t.Fatal("exact replay charged intake budget", e)
		}
	}
	if e := s.transaction(ctx, true, func(ctx context.Context, tx bolt.ExplicitTransaction) error {
		params := s.params()
		params["bytes"] = int64(proto.Size(first) + proto.Size(second))
		_, e := one(ctx, tx, `MATCH (g:RGGeneration {corpus:$corpus,generation:$generation}) SET g.operations=2,g.bytes=$bytes RETURN g.operations`, params)
		return e
	}); e != nil {
		t.Fatal(e)
	}
	if _, err = s.VerifyAndSeal(ctx, []*pb.GraphDelta{first}); err == nil {
		t.Fatal("incomplete write set sealed")
	}
	bad := proto.Clone(first).(*pb.GraphDelta)
	bad.Meta.RecordId = "delta:conflict"
	bad.Entities[0].PreferredLabel = "changed"
	if err = s.ApplyGraphDelta(ctx, bad); err == nil {
		t.Fatal("immutable payload overwritten")
	}
	// Newly created nodes preceding the conflict must also be rolled back.
	newEntity := proto.Clone(bad.Entities[1]).(*pb.CanonicalEntity)
	newEntity.Meta.RecordId = "000:new-before-conflict"
	bad.Entities = append(bad.Entities, newEntity)
	bad.ValidationReport.CheckedRecords++
	if err = s.ApplyGraphDelta(ctx, bad); err == nil {
		t.Fatal("conflicting batch committed")
	}
	other := b
	other.Fence++
	stale, err := New(c, other)
	if err != nil {
		t.Fatal(err)
	}
	defer stale.Close(context.Background())
	if err = stale.EnsureSchema(ctx); err != nil {
		t.Fatal(err)
	}
	if err = stale.ApplyGraphDelta(ctx, first); err == nil {
		t.Fatal("different fence owns generation")
	}
	proof, err := s.VerifyAndSeal(ctx, []*pb.GraphDelta{second, first})
	if err != nil || proof.Records != 5 || proof.Edges != 4 || proof.Operations != 2 {
		t.Fatal("exact readback/seal", proof, err)
	}
	if replay, e := s.VerifyAndSeal(ctx, []*pb.GraphDelta{first, second}); e != nil || replay != proof {
		t.Fatal("seal replay drift", e)
	}
	if err = s.ApplyGraphDelta(ctx, first); err != nil {
		t.Fatal("sealed exact replay", err)
	}
	third := proto.Clone(first).(*pb.GraphDelta)
	third.Meta.RecordId = "delta:late"
	if err = s.ApplyGraphDelta(ctx, third); err == nil {
		t.Fatal("late write entered sealed graph")
	}
	mutate := func(query string) {
		t.Helper()
		if e := s.transaction(ctx, true, func(ctx context.Context, tx bolt.ExplicitTransaction) error {
			r, e := tx.Run(ctx, query, s.params())
			if e != nil {
				return e
			}
			_, e = r.Consume(ctx)
			return e
		}); e != nil {
			t.Fatal(e)
		}
	}
	mutate(`MATCH (n:RGRecord {corpus:$corpus,generation:$generation,id:'entity:a'}) SET n.label='corrupt'`)
	if _, err = s.VerifyAndSeal(ctx, []*pb.GraphDelta{first, second}); err == nil {
		t.Fatal("corrupt readback reported ready")
	}
	mutate(`MATCH (n:RGRecord {corpus:$corpus,generation:$generation,id:'entity:a'}) SET n.label='A'`)
	mutate(`MATCH (a:RGRecord {corpus:$corpus,generation:$generation,id:'entity:a'}),(b:RGRecord {corpus:$corpus,generation:$generation,id:'entity:b'}) CREATE (a)-[:WRONG]->(b)`)
	if _, err = s.VerifyAndSeal(ctx, []*pb.GraphDelta{first, second}); err == nil {
		t.Fatal("unexpected adjacency reported ready")
	}
	mutate(`MATCH (a:RGRecord {corpus:$corpus,generation:$generation})-[r:WRONG]->() DELETE r`)
	mutate(`MATCH (a:RGRecord {corpus:$corpus,generation:$generation,id:'entity:a'}) CREATE (x:RGUnexpected {corpus:$corpus,generation:$generation})-[:WRONG]->(a)`)
	if _, err = s.VerifyAndSeal(ctx, []*pb.GraphDelta{first, second}); err == nil {
		t.Fatal("incoming adjacency from wrong node label reported ready")
	}
	mutate(`MATCH (x:RGUnexpected {corpus:$corpus,generation:$generation}) DETACH DELETE x`)
	mutate(`MATCH (a:RGRecord {corpus:$corpus,generation:$generation,id:'assertion:ab'}),(b:RGRecord {corpus:$corpus,generation:$generation,id:'entity:a'}) CREATE (a)-[:RG_SUBJECT {from_seq:$sequence}]->(b)`)
	if _, err = s.VerifyAndSeal(ctx, []*pb.GraphDelta{first, second}); err == nil {
		t.Fatal("duplicate adjacency reported ready")
	}
	mutate(`MATCH (a:RGRecord {corpus:$corpus,generation:$generation,id:'assertion:ab'})-[r:RG_SUBJECT]->() WITH r LIMIT 1 DELETE r`)
	if _, err = s.VerifyAndSeal(ctx, []*pb.GraphDelta{first, second}); err != nil {
		t.Fatal("restored readback", err)
	}
	t.Logf("Neo4j exact records=%d edges=%d operations=%d; shared assertion retained two supports", proof.Records, proof.Edges, proof.Operations)
}
