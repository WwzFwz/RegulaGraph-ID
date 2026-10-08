// Reviews an immutable queued model exchange under a trusted operator's corpus/scope.
// Inspect verifies all four artifacts and model/source/candidate closure. Accept requires
// an explicit exact response hash and revision, builds the existing durable intent, and
// delegates one atomic review/resume transaction. It never calls a model or edits proposals.
// Bounds include total artifact bytes, references and candidates; measure validation/hash,
// storage/queue p95/p99 and review conflicts. Required benchmark targets remain unmeasured.
package workflows

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"unicode/utf8"

	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/domain"
)

type SemanticReviewStore interface {
	LoadSemanticReviewQueue(context.Context, string, string) (domain.SemanticReviewQueue, error)
	LoadIngestionRequest(context.Context, string) (*pb.IngestionRequest, error)
	AcceptSemanticReview(context.Context, domain.SemanticReviewCommit) (bool, error)
}

// Config is server/operator configuration. Never populate Actor, Corpus or AuthScope from
// an HTTP body. The local CLI derives Actor from the current OS account; possession of the
// configured DB credentials grants local operator access, not remote multi-user RBAC.
type SemanticReviewConfig struct {
	Actor, Corpus, AuthScope             string
	Producer                             *pb.ProducerManifest
	ProducerPin                          *pb.ContentHash
	MaximumBytes                         uint64
	MaximumReferences, MaximumCandidates int
}

type SemanticReviewer struct {
	store     SemanticReviewStore
	artifacts SemanticResolutionArtifactReader
	config    SemanticReviewConfig
}

type SemanticReviewView struct {
	Queue      domain.SemanticReviewQueue
	Source     *pb.ExtractionBatch
	Candidates *pb.RegistryCandidateBatch
	Input      *pb.SemanticResolveRequest
	Output     *pb.SemanticResolveResponse
}

func NewSemanticReviewer(store SemanticReviewStore, artifacts SemanticResolutionArtifactReader, cfg SemanticReviewConfig) (*SemanticReviewer, error) {
	if store == nil || artifacts == nil || !schedulerCorpusIDPattern.MatchString(cfg.Actor) ||
		!schedulerCorpusIDPattern.MatchString(cfg.Corpus) || cfg.AuthScope == "" || cfg.MaximumBytes == 0 ||
		cfg.MaximumBytes > 256<<20 || cfg.MaximumReferences <= 0 || cfg.MaximumCandidates <= 0 {
		return nil, errors.New("trusted review identity, dependencies and bounded limits required")
	}
	for _, value := range []proto.Message{cfg.Producer, cfg.ProducerPin} {
		if err := domain.ValidateWire(value, domain.DefaultWireLimits); err != nil {
			return nil, err
		}
	}
	cfg.Producer = proto.Clone(cfg.Producer).(*pb.ProducerManifest)
	cfg.ProducerPin = proto.Clone(cfg.ProducerPin).(*pb.ContentHash)
	return &SemanticReviewer{store: store, artifacts: artifacts, config: cfg}, nil
}

func (r *SemanticReviewer) Inspect(ctx context.Context, jobID string) (*SemanticReviewView, error) {
	if ctx == nil || !schedulerCorpusIDPattern.MatchString(jobID) {
		return nil, errors.New("valid job ID required")
	}
	queue, err := r.store.LoadSemanticReviewQueue(ctx, r.config.Corpus, jobID)
	if err != nil {
		return nil, err
	}
	if queue.CorpusID != r.config.Corpus || queue.JobID != jobID || !schedulerCorpusIDPattern.MatchString(queue.SourceCheckpointID) {
		return nil, domain.ErrPersistentIntegrity
	}
	view := &SemanticReviewView{Queue: queue, Source: new(pb.ExtractionBatch), Candidates: new(pb.RegistryCandidateBatch), Input: new(pb.SemanticResolveRequest), Output: new(pb.SemanticResolveResponse)}
	remaining := r.config.MaximumBytes
	seen := make(map[string]bool)
	for _, entry := range []struct {
		ref     *pb.ArtifactRef
		message proto.Message
	}{
		{queue.Source, view.Source}, {queue.Candidates, view.Candidates}, {queue.Input, view.Input}, {queue.Output, view.Output},
	} {
		if domain.ValidateWire(entry.ref, domain.DefaultWireLimits) != nil || seen[entry.ref.ArtifactId] || !resolutionProtoMedia(entry.ref.MediaType, entry.message) || entry.ref.ByteSize > remaining {
			return nil, domain.ErrPersistentIntegrity
		}
		seen[entry.ref.ArtifactId] = true
		raw, readErr := r.artifacts.ReadVerified(ctx, entry.ref, remaining)
		if readErr != nil {
			return nil, readErr
		}
		remaining -= entry.ref.ByteSize
		if err = domain.DecodeWire(raw, entry.message, domain.DefaultWireLimits); err != nil {
			return nil, err
		}
	}
	request, err := r.store.LoadIngestionRequest(ctx, jobID)
	if err != nil {
		return nil, err
	}
	if request.GetCorpusId() != queue.CorpusID || !containsExpectedHash(request.GetConfigManifest().GetInputHashes(), r.config.ProducerPin) ||
		view.Source.GetContext().GetAuthScopeRef() != r.config.AuthScope ||
		!proto.Equal(view.Source.GetContext().GetConfigFingerprint(), request.GetConfigManifest().GetConfigHash()) ||
		!proto.Equal(view.Input.GetBatch().GetContext().GetSnapshotRef(), view.Source.GetContext().GetSnapshotRef()) ||
		view.Input.GetBatch().GetContext().GetAuthScopeRef() != r.config.AuthScope ||
		view.Input.GetBatch().GetContext().GetCorpusId() != queue.CorpusID ||
		!proto.Equal(view.Input.GetBatch().GetContext().GetConfigFingerprint(), view.Source.GetContext().GetConfigFingerprint()) ||
		view.Input.GetBatch().GetOntologyVersion() != view.Source.OntologyVersion ||
		!modelProducerMatches(r.config.Producer, view.Input.Batch) {
		return nil, domain.ErrPersistentIntegrity
	}
	if err = domain.ValidateRegistryCandidateBatch(view.Candidates, view.Source, queue.Source, r.config.MaximumReferences, r.config.MaximumCandidates); err != nil {
		return nil, err
	}
	if err = validateReviewInputBinding(view); err != nil {
		return nil, err
	}
	for _, item := range view.Input.Items {
		if err = domain.ValidateSemanticResolutionItem(item, queue.CorpusID, r.config.MaximumReferences); err != nil {
			return nil, err
		}
	}
	if err = validateModelResolutionResponse(view.Input, view.Output, r.config.Producer, view.Source, queue.Source, view.Candidates, r.config.MaximumReferences, r.config.MaximumCandidates); err != nil {
		return nil, err
	}
	return view, nil
}

// A self-consistent model item is insufficient: its mention and complete candidate set
// must match the authoritative EXTRACT/candidate batch, including alias support ownership.
func validateReviewInputBinding(v *SemanticReviewView) error {
	mentions := make(map[string]*pb.Mention, len(v.Source.Mentions))
	for _, mention := range v.Source.Mentions {
		mentions[mention.Meta.RecordId] = mention
	}
	entities := make(map[string]*pb.CanonicalEntity, len(v.Candidates.Candidates))
	for _, entity := range v.Candidates.Candidates {
		entities[entity.Meta.RecordId] = entity
	}
	allowed := make(map[string]map[string]bool, len(v.Candidates.Lookups))
	for _, lookup := range v.Candidates.Lookups {
		set := make(map[string]bool)
		for _, scope := range lookup.Scopes {
			for _, id := range scope.CandidateIds {
				set[id] = true
			}
		}
		allowed[lookup.MentionId] = set
	}
	aliases := make(map[string]*pb.Alias, len(v.Candidates.Aliases))
	aliasSupports := make(map[string]map[string]bool, len(v.Candidates.Aliases))
	for _, alias := range v.Candidates.Aliases {
		aliases[alias.Meta.RecordId] = alias
		supports := make(map[string]bool, len(alias.SupportRefs))
		for _, ref := range alias.SupportRefs {
			supports[ref] = true
		}
		aliasSupports[alias.Meta.RecordId] = supports
	}
	for _, item := range v.Input.Items {
		id := item.GetMention().GetMeta().GetRecordId()
		if mentions[id] == nil || item.ItemId != id || !proto.Equal(item.Mention, mentions[id]) || item.ExpectedRegistryRevision != v.Candidates.RegistryRevision || len(item.Candidates) != len(allowed[id]) {
			return domain.ErrPersistentIntegrity
		}
		delete(mentions, id)
		seen := make(map[string]bool, len(item.Candidates))
		for _, entity := range item.Candidates {
			key := entity.GetMeta().GetRecordId()
			if seen[key] || !allowed[id][key] || !proto.Equal(entity, entities[key]) {
				return domain.ErrPersistentIntegrity
			}
			seen[key] = true
		}
		for _, support := range item.CandidateContexts {
			alias := aliases[support.AliasId]
			if alias == nil || alias.CanonicalId != support.CanonicalId || !allowed[id][support.CanonicalId] {
				return domain.ErrPersistentIntegrity
			}
			if !aliasSupports[support.AliasId][support.SupportMention.GetMeta().GetRecordId()] {
				return domain.ErrPersistentIntegrity
			}
		}
	}
	if len(mentions) != 0 {
		return domain.ErrPersistentIntegrity
	}
	return nil
}

// Accept approves the entire unchanged LINK/DEFER set after human inspection. A false
// return means exact replay, not rejection; errors mean nothing new was accepted.
func (r *SemanticReviewer) Accept(ctx context.Context, jobID, outputHash string, revision uint64, reason string) (bool, error) {
	if strings.TrimSpace(reason) == "" || len(reason) > 1024 || !utf8.ValidString(reason) || strings.ContainsRune(reason, 0) {
		return false, errors.New("explicit UTF-8 review reason (1..1024 bytes) required")
	}
	view, err := r.Inspect(ctx, jobID)
	if err != nil {
		return false, err
	}
	if outputHash != view.Queue.Output.ContentHash.Sha256 || revision != view.Candidates.RegistryRevision || revision == 0 {
		return false, errors.New("review hash or registry revision differs from inspected proposal")
	}
	request := &pb.RegistryResolveRequest{Context: proto.Clone(view.Input.Batch.Context).(*pb.RequestContext), OperationKey: view.Input.Batch.OperationKey, ExpectedRevision: revision}
	approvals := []domain.ReviewedLink{}
	for _, result := range view.Output.Results {
		proposal := proto.Clone(result.GetProposal()).(*pb.ResolutionProposal)
		request.Proposals = append(request.Proposals, proposal)
		if proposal.Action == pb.ResolutionAction_RESOLUTION_ACTION_LINK {
			digest := sha256.Sum256([]byte(jobID + "\x00" + outputHash + "\x00" + proposal.Meta.RecordId))
			approvals = append(approvals, domain.ReviewedLink{ProposalID: proposal.Meta.RecordId, CanonicalID: proposal.CandidateIds[0], Actor: r.config.Actor, Reason: reason, ReviewID: "review:semantic:" + hex.EncodeToString(digest[:])})
		}
	}
	receipt, err := domain.PreviewSemanticResolutionReceipt(request, approvals)
	if err != nil {
		return false, err
	}
	digest := sha256.Sum256([]byte("resolve-executor-v1\x00" + jobID))
	preview, err := domain.AssembleResolutionBatchFromReceipt(view.Source, view.Queue.Source, view.Candidates, view.Queue.Candidates, request, receipt, view.Output.Model, r.config.Producer, view.Output.Usage, "resolution:"+hex.EncodeToString(digest[:]), r.config.MaximumReferences, r.config.MaximumCandidates)
	if err != nil {
		return false, err
	}
	return r.store.AcceptSemanticReview(ctx, domain.SemanticReviewCommit{Queue: view.Queue, Actor: r.config.Actor, Reason: reason, Intent: domain.SemanticResolutionIntent{SourceCheckpointID: view.Queue.SourceCheckpointID, CandidateRef: view.Queue.Candidates, Request: request, Approvals: approvals, Preview: preview}})
}
