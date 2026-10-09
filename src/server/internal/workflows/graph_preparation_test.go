// Tests full-selection preflight before publication side effects. A later source
// that is unready or newer than the target blocks the whole inventory;
// two older compatible revisions proceed to target reservation together;
// oversized candidate metadata is rejected before candidate bytes or reservation.
// Fixtures exercise coordinator invariants, not registry/model correctness.
package workflows

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/domain"
)

type preparationPreflightFixture struct {
	GraphPreparationStore
	sources                          []domain.IndexSourceBinding
	cp                               *pb.Checkpoint
	artifacts                        map[string]domain.GraphSourceArtifact
	intent                           domain.SemanticResolutionIntent
	second                           *pb.Checkpoint
	reads, checkpoints, reservations int
	targetRevision                   uint64
}

func (f *preparationPreflightFixture) GraphPreparationRegistryRevision(context.Context, string, string) (uint64, error) {
	if f.targetRevision != 0 {
		return f.targetRevision, nil
	}
	return 7, nil
}

func (f *preparationPreflightFixture) VerifyRetainedRegistryRevision(context.Context, string, uint64) error {
	return nil
}

func (f *preparationPreflightFixture) LoadPublishedGraphSources(context.Context, domain.SnapshotPin, string) ([]domain.IndexSourceBinding, error) {
	return f.sources, nil
}
func (f *preparationPreflightFixture) LoadLatestCheckpoint(_ context.Context, job string) (*pb.Checkpoint, error) {
	f.checkpoints++
	if job == "job:z" {
		return f.second, nil
	}
	return f.cp, nil
}
func (f *preparationPreflightFixture) LoadArtifact(_ context.Context, _, id string) (*pb.ArtifactRef, error) {
	a, ok := f.artifacts[id]
	if !ok {
		return nil, errors.New("missing artifact")
	}
	return a.Reference, nil
}
func (f *preparationPreflightFixture) VerifyGraphAssemblySourceCheckpoint(context.Context, string, string, string, *pb.ArtifactRef) error {
	return nil
}
func (f *preparationPreflightFixture) LoadSemanticResolutionIntent(context.Context, string, string) (domain.SemanticResolutionIntent, error) {
	return f.intent, nil
}
func (f *preparationPreflightFixture) ReadVerified(_ context.Context, ref *pb.ArtifactRef, _ uint64) ([]byte, error) {
	f.reads++
	a, ok := f.artifacts[ref.ArtifactId]
	if !ok {
		return nil, errors.New("unexpected candidate I/O")
	}
	return a.Bytes, nil
}
func (f *preparationPreflightFixture) Put(context.Context, *pb.ArtifactRef, io.Reader) (bool, error) {
	return false, errors.New("unexpected write")
}
func (f *preparationPreflightFixture) ReservePublication(context.Context, string, string, string, string, string) (domain.PublicationReservation, error) {
	f.reservations++
	return domain.PublicationReservation{}, errors.New("unexpected reserve")
}

func TestGraphPreparationPreflightsEntireSelection(t *testing.T) {
	for _, mode := range []string{"later incomplete", "later revision", "candidate budget", "mixed historical revisions"} {
		t.Run(mode, func(t *testing.T) {
			items := graphEnvelopeFixture(t, mode == "candidate budget")
			resolution := new(pb.ResolutionBatch)
			if err := proto.Unmarshal(items[3].Bytes, resolution); err != nil {
				t.Fatal(err)
			}
			if mode != "candidate budget" {
				resolution.Dependencies.LookupScopeRevisions = nil
			}
			items[3] = graphEnvelopeArtifact(t, resolution, items[3].Reference.ArtifactId, items[3].Reference.MediaType)
			doc := new(pb.DocumentBatch)
			if err := proto.Unmarshal(items[1].Bytes, doc); err != nil {
				t.Fatal(err)
			}
			f := &preparationPreflightFixture{artifacts: map[string]domain.GraphSourceArtifact{}}
			if mode == "mixed historical revisions" {
				f.targetRevision = 9
			}
			for _, a := range items {
				f.artifacts[a.Reference.ArtifactId] = a
			}
			f.sources = []domain.IndexSourceBinding{{PublicationID: "publication:base", Fence: 1, SourceJobID: "job:a", Snapshot: doc.Context.SnapshotRef, AuthScope: doc.Context.AuthScopeRef, Original: items[0].Reference, Bound: items[1].Reference}}
			f.cp = &pb.Checkpoint{Meta: &pb.RecordMeta{SchemaVersion: 1, CorpusId: doc.Meta.CorpusId, RecordId: "checkpoint:a"}, JobId: "job:a", Stage: pb.JobStage_JOB_STAGE_RESOLVE, Fence: 1,
				CompletedBatchKeys: []string{items[3].Reference.ArtifactId}, ArtifactHashes: []*pb.ContentHash{items[3].Reference.ContentHash}, Manifest: resolution.Dependencies.ProducerManifest, TerminalStatus: pb.CompletionStatus_COMPLETION_STATUS_SUCCEEDED}
			if mode == "candidate budget" {
				ref := proto.Clone(items[2].Reference).(*pb.ArtifactRef)
				ref.ArtifactId = "candidate:huge"
				ref.ByteSize = 64 << 20
				f.intent.CandidateRef = ref
			} else {
				second := f.sources[0]
				second.SourceJobID = "job:z"
				f.sources = append(f.sources, second)
				f.second = proto.Clone(f.cp).(*pb.Checkpoint)
				f.second.JobId = "job:z"
				f.second.Meta.RecordId = "checkpoint:z"
				if mode == "later incomplete" {
					f.second.Stage = pb.JobStage_JOB_STAGE_EXTRACT
				} else {
					r := proto.Clone(resolution).(*pb.ResolutionBatch)
					r.RegistryRevision++
					a := graphEnvelopeArtifact(t, r, "resolution:later", items[3].Reference.MediaType)
					f.artifacts[a.Reference.ArtifactId] = a
					f.second.CompletedBatchKeys = []string{a.Reference.ArtifactId}
					f.second.ArtifactHashes = []*pb.ContentHash{a.Reference.ContentHash}
				}
			}
			pin := domain.SnapshotPin{CorpusID: doc.Meta.CorpusId, SnapshotID: doc.Context.SnapshotRef.SnapshotId, Sequence: 1, ExpiresAt: time.Now().Add(time.Minute)}
			cfg := GraphPreparationConfig{CorpusID: pin.CorpusID, PublicationID: "publication:next", SnapshotID: "snapshot:next", BaseSnapshotID: pin.SnapshotID, AuthScope: doc.Context.AuthScopeRef,
				Producer: resolution.Dependencies.ProducerManifest, OntologyHash: parseTestOntology().ContentHash(), MaximumReferences: 4096, MaximumCandidates: 32}
			_, err := PrepareGraphInventory(context.Background(), f, f, f, pin, cfg)
			if mode == "mixed historical revisions" {
				if f.checkpoints != 2 || f.reservations != 1 || err == nil || err.Error() != "unexpected reserve" {
					t.Fatal("valid historical sources failed before reservation", f.checkpoints, f.reservations, err)
				}
				return // Stop at reservation fixture; native test covers durable writes.
			}
			if err == nil || f.reservations != 0 {
				t.Fatal("preflight reserved incomplete inventory", err, f.reservations)
			}
			if mode == "candidate budget" {
				if f.reads != 1 || !strings.Contains(err.Error(), "candidate budget") {
					t.Fatal("candidate read before aggregate check", f.reads, err)
				}
			} else if f.checkpoints != 2 {
				t.Fatal("fixture failed before later source", f.checkpoints, err)
			}
			if mode == "later revision" && !errors.Is(err, domain.ErrResolutionReplan) {
				t.Fatal("cross revision not rejected", err)
			}
		})
	}
}
