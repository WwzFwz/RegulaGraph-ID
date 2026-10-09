// Validates shared ASSEMBLE plans and revision-pinned canonical selections before
// expensive artifact or graph work. These gates prove local shape, exact selection
// coverage and role bindings; storage/worker callers authenticate artifact bytes,
// publisher fences, registry receipts and source checkpoints separately. No model
// or storage I/O occurs here. Measure admission p95/RSS and rejected batches under
// configs/benchmark-targets.yaml; release quality/performance remain UNMEASURED.
package domain

import (
	"errors"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"math"
	pb "regulagraph.local/server/gen/regulagraph/v1"
)

const RegistryEntityViewMediaType = "application/x-protobuf; message=regulagraph.v1.RegistryEntityView"
const GraphAssemblyPlanMediaType = "application/x-protobuf; message=regulagraph.v1.GraphAssemblyPlan"

// RegistryEntityExport is the local control-plane request for an exact historical
// selection. The response remains the shared C01 RegistryEntityView message.
type RegistryEntityExport struct {
	ViewID, CorpusID, PublicationID string
	Fence, Revision                 uint64
	EntityIDs                       []string
	Producer                        *pb.ProducerManifest
	MaximumEntities, MaximumBytes   int
}

// ValidateGraphResolutionSource admits the immutable original RESOLVE chain.
// It checks structure, not committed registry authority or source-text semantics.
func ValidateGraphResolutionSource(source *pb.ExtractionBatch, sourceRef *pb.ArtifactRef, resolution *pb.ResolutionBatch, maximumEdges int) error {
	for _, message := range []proto.Message{source, sourceRef, resolution} {
		if err := ValidateWire(message, DefaultWireLimits); err != nil {
			return err
		}
		if !graphAssemblyKnownFields(message.ProtoReflect()) {
			return errors.New("unknown graph resolution fields")
		}
	}
	if source.Meta.SchemaVersion != 1 || resolution.Meta.SchemaVersion != 1 || source.Meta.Visibility != nil || resolution.Meta.Visibility != nil ||
		source.Completeness != pb.Completeness_COMPLETENESS_COMPLETE || resolution.Completeness != pb.Completeness_COMPLETENESS_COMPLETE {
		return errors.New("complete graph resolution source required")
	}
	return ValidateResolutionBatchClosure(resolution, source, sourceRef, maximumEdges)
}

func ValidateRegistryEntityView(view *pb.RegistryEntityView, maximumEntities, maximumBytes int) error {
	if view == nil || maximumEntities <= 0 || maximumEntities > DefaultWireLimits.MaxItems || maximumBytes <= 0 || maximumBytes > DefaultWireLimits.MaxBytes {
		return errors.New("bounded registry entity view required")
	}
	limits := DefaultWireLimits
	limits.MaxBytes = maximumBytes
	if err := ValidateWire(view, limits); err != nil {
		return err
	}
	if !graphAssemblyKnownFields(view.ProtoReflect()) || view.Meta.SchemaVersion != 1 || view.Meta.Visibility != nil || view.ProducerManifest.SchemaVersion != 1 || view.PublicationFence > math.MaxInt64 || view.RegistryRevision > math.MaxInt64 || len(view.RequestedIds) > maximumEntities || len(view.Entities) != len(view.RequestedIds) {
		return errors.New("registry selection version, budget or coverage mismatch")
	}
	for i, entity := range view.Entities {
		if i > 0 && view.RequestedIds[i-1] >= view.RequestedIds[i] || entity.Meta.RecordId != view.RequestedIds[i] || entity.Meta.RecordId == view.Meta.RecordId || entity.Meta.SchemaVersion != 1 || entity.Meta.Visibility != nil || entity.Meta.CorpusId != view.Meta.CorpusId || entity.RegistryRevision > view.RegistryRevision || CanonicalEntityTypeCode(entity.EntityType) == 0 || entity.ReviewState != pb.ReviewState_REVIEW_STATE_APPROVED && entity.ReviewState != pb.ReviewState_REVIEW_STATE_UNREVIEWED {
			return errors.New("registry entity is missing, reordered, future, or not eligible")
		}
	}
	return nil
}

func ValidateGraphAssemblyPlan(plan *pb.GraphAssemblyPlan) error {
	if plan == nil {
		return errors.New("ASSEMBLE plan required")
	}
	if err := ValidateWire(plan, DefaultWireLimits); err != nil {
		return err
	}
	base := plan.Context.SnapshotRef
	if !graphAssemblyKnownFields(plan.ProtoReflect()) || plan.Meta.SchemaVersion != 1 || plan.Context.SchemaVersion != 1 || plan.ProducerManifest.SchemaVersion != 1 || plan.Meta.Visibility != nil || base == nil || base.CorpusId != plan.Meta.CorpusId || plan.Context.CorpusId != plan.Meta.CorpusId || plan.TargetSequence <= base.Sequence || plan.TargetSequence > math.MaxInt64 || plan.PublicationFence > math.MaxInt64 || plan.RegistryRevision > math.MaxInt64 || plan.OutputArtifactId == plan.Meta.RecordId || !IsDocumentBatchMediaType(plan.DocumentBatch.MediaType) || plan.ExtractionBatch.MediaType != ExtractionBatchMediaType && plan.ExtractionBatch.MediaType != "application/x-protobuf" || plan.ResolutionBatch.MediaType != "application/x-protobuf" && plan.ResolutionBatch.MediaType != "application/x-protobuf; message=regulagraph.v1.ResolutionBatch" || plan.RegistryView.MediaType != RegistryEntityViewMediaType {
		return errors.New("ASSEMBLE plan scope, role, sequence or schema mismatch")
	}
	seen := map[string]bool{plan.Meta.RecordId: true, plan.OutputArtifactId: true}
	remaining := uint64(64 << 20)
	for _, ref := range []*pb.ArtifactRef{plan.DocumentBatch, plan.ExtractionBatch, plan.ResolutionBatch, plan.RegistryView} {
		if ref.SchemaVersion != 1 || ref.ByteSize == 0 || ref.ByteSize > uint64(DefaultWireLimits.MaxBytes) || ref.ByteSize > remaining || seen[ref.ArtifactId] {
			return errors.New("ASSEMBLE artifact roles overlap or exceed budget")
		}
		seen[ref.ArtifactId] = true
		remaining -= ref.ByteSize
	}
	return nil
}

func ValidateAssemblyRegistryBinding(plan *pb.GraphAssemblyPlan, view *pb.RegistryEntityView) error {
	if err := ValidateGraphAssemblyPlan(plan); err != nil {
		return err
	}
	if err := ValidateRegistryEntityView(view, DefaultWireLimits.MaxItems, DefaultWireLimits.MaxBytes); err != nil {
		return err
	}
	if view.Meta.RecordId != plan.RegistryView.ArtifactId || view.Meta.CorpusId != plan.Meta.CorpusId || view.PublicationId != plan.PublicationId || view.PublicationFence != plan.PublicationFence || view.RegistryRevision != plan.RegistryRevision {
		return errors.New("ASSEMBLE registry view differs from publication binding")
	}
	return nil
}

// Called only after bounded wire validation. Stage semantics fail closed on fields
// they cannot interpret, while generic C01 binary transport still preserves them.
func graphAssemblyKnownFields(message protoreflect.Message) bool {
	if len(message.GetUnknown()) != 0 {
		return false
	}
	known := true
	message.Range(func(field protoreflect.FieldDescriptor, value protoreflect.Value) bool {
		if field.IsMap() {
			known = false
			return false
		}
		if field.Message() == nil {
			return true
		}
		if field.IsList() {
			values := value.List()
			for i := 0; i < values.Len(); i++ {
				if !graphAssemblyKnownFields(values.Get(i).Message()) {
					known = false
					return false
				}
			}
		} else {
			known = graphAssemblyKnownFields(value.Message())
		}
		return known
	})
	return known
}
