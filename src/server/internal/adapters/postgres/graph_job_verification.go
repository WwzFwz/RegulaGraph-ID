// Authenticates the full immutable source/decision/view inventory before ASSEMBLE
// scheduling. Expensive decoding, hashes and historical reads happen before locks.
// The resulting opaque admission owns cloned plans plus the observed registry
// revision/history floor. Schedule must compare them under the corpus lock and
// recheck publication/checkpoint/membership before creating any child. This avoids
// nested pool acquisition under locks, including a one-connection pool. No model
// inference is repeated. Measure read/hash/lock p95 and retries under required gates.
package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"

	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/domain"
)

type GraphJobAdmission struct {
	repository             *Repository
	inventory              domain.GraphJobInventory
	bindings               []domain.GraphSourceBinding
	revision, historyFloor int64
	payload                []byte
	digest                 string
}

func (r *Repository) PrepareGraphJobAdmission(ctx context.Context, inventory domain.GraphJobInventory,
	inputs map[string]domain.GraphJobSourceInputs, maximumReferences, maximumCandidates int) (*GraphJobAdmission, error) {
	if ctx == nil || maximumReferences <= 0 || maximumReferences > domain.DefaultWireLimits.MaxItems || maximumCandidates <= 0 || maximumCandidates > domain.DefaultWireLimits.MaxItems {
		return nil, errors.New("bounded graph inventory admission required")
	}
	if err := domain.ValidateGraphJobInventory(inventory); err != nil {
		return nil, err
	}
	if len(inputs) != len(inventory.Assignments) {
		return nil, errors.New("graph source input coverage mismatch")
	}
	owned := domain.GraphJobInventory{}
	remaining := 64 << 20
	for _, a := range inventory.Assignments {
		input, ok := inputs[a.SourceJobID]
		if !ok {
			return nil, errors.New("missing graph source inputs")
		}
		for _, raw := range [][]byte{input.Source.OriginalDocument, input.Source.SnapshotDocument, input.Source.Extraction, input.Source.Resolution, input.Candidates, input.RegistryView} {
			if len(raw) > remaining {
				return nil, ErrResultLimit
			}
			remaining -= len(raw)
		}
		owned.Assignments = append(owned.Assignments, domain.GraphJobAssignment{JobID: a.JobID, SourceJobID: a.SourceJobID, Plan: proto.Clone(a.Plan).(*pb.GraphAssemblyPlan), Reference: proto.Clone(a.Reference).(*pb.ArtifactRef)})
	}
	result := &GraphJobAdmission{repository: r, inventory: owned}
	corpus := owned.Assignments[0].Plan.Meta.CorpusId
	if err := r.pool.QueryRow(ctx, `SELECT registry_revision,registry_history_floor FROM corpus_state WHERE corpus_id=$1`, corpus).Scan(&result.revision, &result.historyFloor); err != nil {
		return nil, err
	}
	if result.revision <= 0 || result.historyFloor <= 0 || result.historyFloor > result.revision {
		return nil, domain.ErrPersistentIntegrity
	}
	for _, a := range owned.Assignments {
		binding, err := r.verifyGraphJobSource(ctx, a, inputs[a.SourceJobID], maximumReferences, maximumCandidates)
		if err != nil {
			return nil, fmt.Errorf("verify graph job %s: %w", a.JobID, err)
		}
		result.bindings = append(result.bindings, binding)
	}
	var err error
	result.payload, err = json.Marshal(owned)
	if err != nil {
		return nil, err
	}
	if len(result.payload) > 64<<20 {
		return nil, ErrResultLimit
	}
	result.digest = fmt.Sprintf("%x", sha256.Sum256(result.payload))
	return result, nil
}

func (r *Repository) verifyGraphJobSource(ctx context.Context, a domain.GraphJobAssignment, input domain.GraphJobSourceInputs, refs, candidatesLimit int) (domain.GraphSourceBinding, error) {
	var empty domain.GraphSourceBinding
	p := a.Plan
	b, err := r.LoadGraphSourceBinding(ctx, p.Meta.CorpusId, p.PublicationId, a.SourceJobID)
	if err != nil {
		return empty, err
	}
	if b.Fence != p.PublicationFence || b.TargetSequence != p.TargetSequence || b.RegistryRevision != p.RegistryRevision || b.SourceCheckpointID != p.SourceCheckpointId ||
		!proto.Equal(b.Source.Bound, p.DocumentBatch) || !proto.Equal(b.BoundExtraction, p.ExtractionBatch) || !proto.Equal(b.BoundResolution, p.ResolutionBatch) || !proto.Equal(b.Source.Snapshot, p.Context.SnapshotRef) || b.Source.AuthScope != p.Context.AuthScopeRef {
		return empty, ErrConflict
	}
	planRaw, err := proto.MarshalOptions{Deterministic: true}.Marshal(p)
	if err != nil {
		return empty, err
	}
	for _, artifact := range []domain.GraphSourceArtifact{
		{Reference: a.Reference, Bytes: planRaw}, {Reference: b.Source.Original, Bytes: input.Source.OriginalDocument},
		{Reference: b.Source.Bound, Bytes: input.Source.SnapshotDocument}, {Reference: b.OriginalExtraction, Bytes: input.Source.Extraction},
		{Reference: b.OriginalResolution, Bytes: input.Source.Resolution}, {Reference: p.RegistryView, Bytes: input.RegistryView},
	} {
		if err = r.verifyGraphRegisteredBytes(ctx, p.Meta.CorpusId, artifact); err != nil {
			return empty, err
		}
	}
	eBound, rBound, err := domain.BindGraphSourceEnvelopes(a.SourceJobID,
		domain.GraphSourceArtifact{Reference: b.Source.Original, Bytes: input.Source.OriginalDocument}, domain.GraphSourceArtifact{Reference: b.Source.Bound, Bytes: input.Source.SnapshotDocument},
		domain.GraphSourceArtifact{Reference: b.OriginalExtraction, Bytes: input.Source.Extraction}, domain.GraphSourceArtifact{Reference: b.OriginalResolution, Bytes: input.Source.Resolution}, refs)
	if err != nil {
		return empty, err
	}
	if !proto.Equal(eBound.Reference, b.BoundExtraction) || !proto.Equal(rBound.Reference, b.BoundResolution) {
		return empty, domain.ErrPersistentIntegrity
	}
	for _, artifact := range []domain.GraphSourceArtifact{eBound, rBound} {
		if err = r.verifyGraphRegisteredBytes(ctx, p.Meta.CorpusId, artifact); err != nil {
			return empty, err
		}
	}
	document, extract, resolve, view := new(pb.DocumentBatch), new(pb.ExtractionBatch), new(pb.ResolutionBatch), new(pb.RegistryEntityView)
	for _, item := range []struct {
		raw     []byte
		message proto.Message
	}{{input.Source.SnapshotDocument, document}, {input.Source.Extraction, extract}, {input.Source.Resolution, resolve}, {input.RegistryView, view}} {
		if err = domain.DecodeWire(item.raw, item.message, domain.DefaultWireLimits); err != nil {
			return empty, err
		}
	}
	if resolve.RegistryRevision != p.RegistryRevision {
		return empty, domain.ErrResolutionReplan
	}
	if err = domain.ValidateGraphResolutionSource(extract, b.OriginalExtraction, resolve, refs); err != nil {
		return empty, err
	}
	if err = domain.ValidateGraphExtractionDependencies(extract); err != nil {
		return empty, err
	}
	if !proto.Equal(p.Context.ConfigFingerprint, document.Context.ConfigFingerprint) {
		return empty, ErrConflict
	}
	ontology := false
	for _, hash := range extract.Dependencies.ProducerManifest.InputHashes {
		ontology = ontology || proto.Equal(hash, p.OntologyHash)
	}
	if !ontology {
		return empty, errors.New("graph ontology differs from extraction")
	}
	if err = r.VerifyDocumentRegistryView(ctx, p.Meta.CorpusId, b.Source.Bound, input.Source.SnapshotDocument, p.RegistryRevision, refs); err != nil {
		return empty, err
	}
	if len(extract.Mentions) == 0 {
		deps := resolve.Dependencies
		if len(input.Candidates) != 0 || len(deps.LookupScopeRevisions) != 0 || len(deps.Dependencies) != 1 || deps.Dependencies[0].DependencyId != b.OriginalExtraction.ArtifactId || !proto.Equal(deps.Dependencies[0].Fingerprint, b.OriginalExtraction.ContentHash) {
			return empty, errors.New("empty RESOLVE has unsupported dependencies")
		}
	} else {
		intent, e := r.LoadSemanticResolutionIntent(ctx, p.Meta.CorpusId, a.SourceJobID)
		if e != nil {
			return empty, e
		}
		if intent.Request == nil || !proto.Equal(intent.Preview, resolve) {
			return empty, domain.ErrPersistentIntegrity
		}
		semantic := domain.SemanticRegistryInputs{SourceRef: b.OriginalExtraction, SourceBytes: input.Source.Extraction, CandidateRef: intent.CandidateRef, CandidateBytes: input.Candidates}
		_, candidateBatch, e := r.decodeSemanticInputs(ctx, p.Meta.CorpusId, semantic)
		if e != nil {
			return empty, e
		}
		receipt, e := r.ReadCommittedSemanticResolution(ctx, semantic, intent.Request, intent.Approvals, refs, candidatesLimit)
		if e != nil {
			return empty, e
		}
		reconstructed, e := domain.AssembleResolutionBatchFromReceipt(extract, b.OriginalExtraction, candidateBatch, intent.CandidateRef, intent.Request, receipt, resolve.ModelManifest, resolve.Dependencies.ProducerManifest, resolve.TokenUsage, resolve.Meta.RecordId, refs, candidatesLimit)
		if e != nil {
			return empty, e
		}
		if !proto.Equal(reconstructed, resolve) {
			return empty, domain.ErrPersistentIntegrity
		}
		aliasLimit, e := domain.GraphCandidateAliasBudget(candidateBatch, refs)
		if e != nil {
			return empty, e
		}
		if e = r.VerifyRegistryCandidateView(ctx, p.Meta.CorpusId, semantic, p.RegistryRevision, refs, aliasLimit, refs, candidatesLimit); e != nil {
			return empty, e
		}
	}
	selected, err := domain.GraphAssemblyCanonicalSelection(resolve)
	if err != nil {
		return empty, err
	}
	if err = domain.ValidateAssemblyRegistryBinding(p, view); err != nil {
		return empty, err
	}
	expected, err := r.ExportRegistryEntityView(ctx, domain.RegistryEntityExport{ViewID: view.Meta.RecordId, CorpusID: p.Meta.CorpusId, PublicationID: p.PublicationId, Fence: p.PublicationFence, Revision: p.RegistryRevision, EntityIDs: selected, Producer: p.ProducerManifest, MaximumEntities: refs, MaximumBytes: domain.DefaultWireLimits.MaxBytes})
	if err != nil {
		return empty, err
	}
	if !proto.Equal(expected, view) {
		return empty, domain.ErrPersistentIntegrity
	}
	remaining := uint64(domain.DefaultWireLimits.MaxBytes)
	for _, ref := range []*pb.ArtifactRef{a.Reference, p.DocumentBatch, p.ExtractionBatch, p.ResolutionBatch, p.RegistryView} {
		if ref.ByteSize > remaining {
			return empty, ErrResultLimit
		}
		remaining -= ref.ByteSize
	}
	if err = domain.BudgetGraphAssemblyTexts(document, extract, &remaining); err != nil {
		return empty, err
	}
	if err = r.VerifyGraphAssemblyPublication(ctx, p); err != nil {
		return empty, err
	}
	return b, nil
}

func (r *Repository) verifyGraphRegisteredBytes(ctx context.Context, corpus string, artifact domain.GraphSourceArtifact) error {
	ref := artifact.Reference
	if err := domain.ValidateWire(ref, domain.DefaultWireLimits); err != nil {
		return err
	}
	if len(artifact.Bytes) == 0 || len(artifact.Bytes) > domain.DefaultWireLimits.MaxBytes || ref.ByteSize != uint64(len(artifact.Bytes)) || ref.ContentHash.Sha256 != fmt.Sprintf("%x", sha256.Sum256(artifact.Bytes)) {
		return domain.ErrPersistentIntegrity
	}
	stored, err := r.LoadArtifact(ctx, corpus, ref.ArtifactId)
	if err != nil {
		return err
	}
	if !proto.Equal(stored, ref) {
		return domain.ErrPersistentIntegrity
	}
	return nil
}
