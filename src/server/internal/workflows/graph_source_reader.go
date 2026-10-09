// Reads the exact source projection shared by live ASSEMBLE and publication
// preparation. Plan, source roles and normalized evidence text are hash-checked
// with the same 16MiB per-assignment budget; no worker/model call is made here.
// Domain owns semantic projection validation; callers own live storage authority.
// Measure source read/hash time and RSS under configs/benchmark-targets.yaml.
package workflows

import (
	"context"
	"errors"

	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/domain"
)

func readGraphSourceProjection(ctx context.Context, reader DocumentArtifactReader, a domain.GraphJobAssignment, ontology *domain.Ontology) (domain.GraphOutputSources, error) {
	var err error
	remaining := uint64(domain.DefaultWireLimits.MaxBytes)
	read := func(ref *pb.ArtifactRef, m proto.Message) error {
		raw, err := readGraphBytes(ctx, reader, ref, &remaining)
		if err != nil {
			return err
		}
		if err := domain.DecodeWire(raw, m, domain.DefaultWireLimits); err != nil {
			return errors.Join(domain.ErrPersistentIntegrity, err)
		}
		return nil
	}
	plan := new(pb.GraphAssemblyPlan)
	if err = read(a.Reference, plan); err != nil {
		return domain.GraphOutputSources{}, err
	}
	if !proto.Equal(plan, a.Plan) || !proto.Equal(plan.OntologyHash, ontology.ContentHash()) {
		return domain.GraphOutputSources{}, errors.Join(domain.ErrPersistentIntegrity, errors.New("stored graph plan or ontology drift"))
	}
	in := domain.GraphOutputSources{Document: new(pb.DocumentBatch), Extraction: new(pb.ExtractionBatch), Resolution: new(pb.ResolutionBatch), Registry: new(pb.RegistryEntityView), NormalizedTexts: map[string][]byte{}}
	for _, role := range []struct {
		ref    *pb.ArtifactRef
		target proto.Message
	}{
		{plan.DocumentBatch, in.Document}, {plan.ExtractionBatch, in.Extraction}, {plan.ResolutionBatch, in.Resolution}, {plan.RegistryView, in.Registry},
	} {
		if err = read(role.ref, role.target); err != nil {
			return domain.GraphOutputSources{}, err
		}
	}
	if err = domain.ValidateExtractionBatchClosure(in.Extraction, in.Document, domain.DefaultWireLimits.MaxItems); err != nil {
		return domain.GraphOutputSources{}, errors.Join(domain.ErrPersistentIntegrity, err)
	}
	// Precharge required text descriptors before any text I/O, then read each once.
	textBudget := remaining
	if err = domain.BudgetGraphAssemblyTexts(in.Document, in.Extraction, &textBudget); err != nil {
		return domain.GraphOutputSources{}, errors.Join(domain.ErrPersistentIntegrity, err)
	}
	needed := map[string]bool{}
	for _, m := range in.Extraction.Mentions {
		needed[m.TextSpan.TextArtifactId] = true
	}
	for _, s := range in.Extraction.Supports {
		for _, span := range s.EvidenceSpans {
			needed[span.TextArtifactId] = true
		}
	}
	for _, text := range in.Document.TextArtifacts {
		if !needed[text.Meta.RecordId] {
			continue
		}
		raw, err := readGraphBytes(ctx, reader, text.NormalizedTextRef, &remaining)
		if err != nil {
			return domain.GraphOutputSources{}, err
		}
		in.NormalizedTexts[text.Meta.RecordId] = raw
	}
	return in, nil
}
