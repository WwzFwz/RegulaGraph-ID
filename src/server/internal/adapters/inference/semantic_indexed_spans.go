// Projects EXTRACT v3 source-unit ranges to the existing exact C01 byte spans.
// The gateway, not the model, owns surface text and source identity. Ranges are
// inclusive 1-based IDs for this immutable item; malformed/out-of-range indices
// reject the entire item, with no clamping, fuzzy repair or legacy fallback.
// Shared projection still enforces provenance, ontology and support closure.
// Aggregate expanded text is bounded; measure failure/coverage, allocation,
// latency and extraction quality per configs/benchmark-targets.yaml.
package inference

import (
	"encoding/json"
	"errors"
	"fmt"

	pb "regulagraph.local/server/gen/regulagraph/v1"
)

type indexedSpan struct {
	First *uint64 `json:"first_token"`
	Last  *uint64 `json:"last_token"`
}

type indexedMention struct {
	LocalID       string       `json:"local_id"`
	CandidateType string       `json:"candidate_type"`
	Span          *indexedSpan `json:"span"`
}

type indexedSupport struct {
	AssertionLocalID string         `json:"assertion_local_id"`
	Spans            *[]indexedSpan `json:"spans"`
}

type indexedProposal struct {
	Mentions   *[]indexedMention `json:"mentions"`
	Assertions *[]rawAssertion   `json:"assertions"`
	Supports   *[]indexedSupport `json:"supports"`
	Warnings   json.RawMessage   `json:"warnings"`
}

func indexedRelativeSpan(text string, units []sourceUnit, span *indexedSpan, remaining *int, copies int) (rawSpan, error) {
	if span == nil || span.First == nil || span.Last == nil || *span.First == 0 ||
		*span.Last < *span.First || *span.Last > uint64(len(units)) {
		return rawSpan{}, errors.New("source unit range must be present, ordered and within this item")
	}
	start, end := units[*span.First-1].start, units[*span.Last-1].end
	if end-start > *remaining/copies {
		return rawSpan{}, errors.New("indexed projection exceeds aggregate source byte budget")
	}
	*remaining -= (end - start) * copies
	first, last := uint64(start), uint64(end)
	return rawSpan{Start: &first, End: &last, Quote: text[start:end]}, nil
}

func projectIndexedExtractionProposal(request *pb.ExtractBatchRequest, item *pb.TextItem, raw json.RawMessage, producer *pb.ProducerManifest, ontologyVersion string) (*pb.ExtractionProposal, error) {
	// Shared strict JSON scanner rejects duplicate/noncanonical keys and depth.
	if err := validateQuotedJSON(raw); err != nil {
		return nil, err
	}
	var value indexedProposal
	if err := decodeStrict(raw, &value); err != nil {
		return nil, err
	}
	if value.Mentions == nil || value.Assertions == nil || value.Supports == nil {
		return nil, errors.New("mentions, assertions and supports must be arrays")
	}
	units, err := sourceLexicalUnits(item.Text)
	if err != nil {
		return nil, err
	}
	remaining := maximumIndexedProjectionBytes
	mentions := make([]rawMention, 0, len(*value.Mentions))
	supports := make([]rawSupport, 0, len(*value.Supports))
	for i, mention := range *value.Mentions {
		span, err := indexedRelativeSpan(item.Text, units, mention.Span, &remaining, 2)
		if err != nil {
			return nil, fmt.Errorf("mentions[%d].span: %w", i, err)
		}
		mentions = append(mentions, rawMention{LocalID: mention.LocalID, CandidateType: mention.CandidateType, SurfaceForm: span.Quote, Span: &span})
	}
	for i, support := range *value.Supports {
		if support.Spans == nil {
			return nil, fmt.Errorf("supports[%d].spans must be an array", i)
		}
		spans := make([]rawSpan, 0, len(*support.Spans))
		for j := range *support.Spans {
			span, err := indexedRelativeSpan(item.Text, units, &(*support.Spans)[j], &remaining, 1)
			if err != nil {
				return nil, fmt.Errorf("supports[%d].spans[%d]: %w", i, j, err)
			}
			spans = append(spans, span)
		}
		supports = append(supports, rawSupport{AssertionLocalID: support.AssertionLocalID, Spans: &spans})
	}
	return projectRawExtractionProposal(request, item, rawProposal{Mentions: &mentions, Assertions: value.Assertions, Supports: &supports, Warnings: value.Warnings}, producer, ontologyVersion)
}
