// Package domain validates the paired legal-version filter metadata on new X01
// index records before a backend writer can publish them. C01 wire validation
// checks field shape; this stage gate checks closure and visibility between
// fields. The caller must still prove version/status/source authenticity from a
// verified DocumentBatch and bind the record to a committed snapshot. This
// bounded pass is O(number of provision refs + filters); measure its batch p95,
// throughput and allocation against configs/benchmark-targets.yaml.
package domain

import (
	"errors"
	"fmt"

	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
)

// ValidatePairedIndexGeneration rejects a representation whose legal filter
// format is absent or unknown to this reader/writer. It is an admission check,
// not proof that a legacy executable can safely serve the new collection.
func ValidatePairedIndexGeneration(generation *pb.IndexGeneration) error {
	if generation == nil {
		return errors.New("index generation is required")
	}
	if err := ValidateWire(generation, DefaultWireLimits); err != nil {
		return fmt.Errorf("invalid index generation: %w", err)
	}
	if generation.FilterFormat != pb.IndexFilterFormat_INDEX_FILTER_FORMAT_PAIRED_PROVISION_V1 {
		return errors.New("index generation legal filter format is unsupported")
	}
	return nil
}

// ValidatePairedIndexFilters is required for newly written IndexRecords.
// Historical records with only legacy parallel arrays remain wire-readable,
// but they cannot be admitted through this publication gate.
func ValidatePairedIndexFilters(record *pb.IndexRecord) error {
	if record == nil {
		return errors.New("index record is required")
	}
	if err := ValidateWire(record, DefaultWireLimits); err != nil {
		return fmt.Errorf("invalid index record: %w", err)
	}
	metadata := record.FilterMetadata
	if record.Meta.Visibility == nil || metadata.Visibility == nil ||
		!proto.Equal(record.Meta.Visibility, metadata.Visibility) {
		return errors.New("index record and filter visibility differ")
	}
	if len(metadata.RegulationIds) != 0 || len(metadata.SourceBlobIds) != 0 ||
		len(metadata.LegalIntervals) != 0 || len(metadata.LegalStatuses) != 0 ||
		len(metadata.Jurisdictions) != 0 {
		return errors.New("new index writer cannot mix paired filters with legacy arrays")
	}
	versions := make(map[string]bool, len(record.ProvisionVersionRefs))
	for _, id := range record.ProvisionVersionRefs {
		versions[id] = false
	}
	seenPairs := make(map[string]map[string]bool, len(versions))
	for _, filter := range metadata.ProvisionFilters {
		if filter == nil {
			return errors.New("nil provision filter")
		}
		if _, exists := versions[filter.ProvisionVersionId]; !exists {
			return errors.New("filter references a version outside the index record")
		}
		if seenPairs[filter.ProvisionVersionId] == nil {
			seenPairs[filter.ProvisionVersionId] = make(map[string]bool)
		}
		if seenPairs[filter.ProvisionVersionId][filter.SourceBlobId] {
			return errors.New("duplicate version/source filter pair")
		}
		seenPairs[filter.ProvisionVersionId][filter.SourceBlobId] = true
		versions[filter.ProvisionVersionId] = true
	}
	for _, covered := range versions {
		if !covered {
			return errors.New("index version lacks paired legal filter")
		}
	}
	return nil
}
