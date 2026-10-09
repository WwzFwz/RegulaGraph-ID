// Projects the versioned quote-context extraction format into the existing C01 byte-span contract.
// Exact, unique prefix+quote+suffix matches determine UTF-8 offsets; no normalization, fuzzy
// repair, occurrence guessing, or fallback from invalid v1 offsets is allowed. Shared projection
// retains source IDs, absolute spans, ontology checks, and support closure. Per-item cached searches
// have a bounded scan budget; measure alignment failures and throughput with model quality under
// configs/benchmark-targets.yaml. Matching text proves location, not legal entailment.
package inference

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"unicode/utf8"

	pb "regulagraph.local/server/gen/regulagraph/v1"
)

const quotedExtractionSchemaID = "https://regulagraph.local/schema/extraction-output-v2.json"
const maximumQuoteContextBytes = 1024
const maximumQuoteScanBytes = 64 << 20

type quotedSpan struct {
	Quote  *string `json:"quote"`
	Prefix *string `json:"prefix"`
	Suffix *string `json:"suffix"`
}

type quotedMention struct {
	LocalID       string      `json:"local_id"`
	SurfaceForm   string      `json:"surface_form"`
	CandidateType string      `json:"candidate_type"`
	Span          *quotedSpan `json:"span"`
}

type quotedSupport struct {
	AssertionLocalID string        `json:"assertion_local_id"`
	Spans            *[]quotedSpan `json:"spans"`
}

type quotedProposal struct {
	Mentions   *[]quotedMention `json:"mentions"`
	Assertions *[]rawAssertion  `json:"assertions"`
	Supports   *[]quotedSupport `json:"supports"`
	Warnings   json.RawMessage  `json:"warnings"`
}

type quoteAlignment struct {
	text      string
	remaining int
	positions map[string]int
}

func (a *quoteAlignment) relative(span *quotedSpan) (rawSpan, error) {
	if span == nil || span.Quote == nil || span.Prefix == nil || span.Suffix == nil || *span.Quote == "" {
		return rawSpan{}, errors.New("quote, prefix and suffix must be present strings with nonempty quote")
	}
	quote, prefix, suffix := *span.Quote, *span.Prefix, *span.Suffix
	if !utf8.ValidString(quote) || !utf8.ValidString(prefix) || !utf8.ValidString(suffix) ||
		len(prefix) > maximumQuoteContextBytes || len(suffix) > maximumQuoteContextBytes ||
		utf8.RuneCountInString(prefix) > 256 || utf8.RuneCountInString(suffix) > 256 ||
		len(quote) > len(a.text) || len(prefix)+len(quote)+len(suffix) > len(a.text) {
		return rawSpan{}, errors.New("quote context exceeds source or encoding bounds")
	}
	anchor := prefix + quote + suffix
	position, found := a.positions[anchor]
	if !found {
		// Each uncached anchor can scan the source twice, stopping at the second match.
		if len(a.text) > a.remaining/2 {
			return rawSpan{}, errors.New("quote alignment scan budget exhausted")
		}
		a.remaining -= 2 * len(a.text)
		position = strings.Index(a.text, anchor)
		if position < 0 {
			return rawSpan{}, errors.New("quote context is absent from source")
		}
		if strings.Index(a.text[position+1:], anchor) >= 0 {
			return rawSpan{}, errors.New("quote context is ambiguous in source")
		}
		a.positions[anchor] = position
	}
	start := uint64(position + len(prefix))
	end := start + uint64(len(quote))
	return rawSpan{Start: &start, End: &end, Quote: quote}, nil
}

func projectQuotedExtractionProposal(request *pb.ExtractBatchRequest, item *pb.TextItem, raw json.RawMessage, producer *pb.ProducerManifest, ontologyVersion string) (*pb.ExtractionProposal, error) {
	if err := validateQuotedJSON(raw); err != nil {
		return nil, err
	}
	var quoted quotedProposal
	if err := decodeStrict(raw, &quoted); err != nil {
		return nil, err
	}
	if quoted.Mentions == nil || quoted.Assertions == nil || quoted.Supports == nil {
		return nil, errors.New("mentions, assertions and supports must be arrays")
	}
	align := quoteAlignment{text: item.Text, remaining: maximumQuoteScanBytes, positions: make(map[string]int)}
	mentions := make([]rawMention, 0, len(*quoted.Mentions))
	supports := make([]rawSupport, 0, len(*quoted.Supports))
	for _, mention := range *quoted.Mentions {
		span, err := align.relative(mention.Span)
		if err != nil {
			return nil, err
		}
		mentions = append(mentions, rawMention{LocalID: mention.LocalID, SurfaceForm: mention.SurfaceForm, CandidateType: mention.CandidateType, Span: &span})
	}
	for _, support := range *quoted.Supports {
		if support.Spans == nil {
			return nil, errors.New("support spans must be an array")
		}
		spans := make([]rawSpan, 0, len(*support.Spans))
		for i := range *support.Spans {
			span, err := align.relative(&(*support.Spans)[i])
			if err != nil {
				return nil, err
			}
			spans = append(spans, span)
		}
		supports = append(supports, rawSupport{AssertionLocalID: support.AssertionLocalID, Spans: &spans})
	}
	return projectRawExtractionProposal(request, item, rawProposal{Mentions: &mentions, Assertions: quoted.Assertions, Supports: &supports, Warnings: quoted.Warnings}, producer, ontologyVersion)
}

// Reject ambiguous duplicate/case-folded keys before Go's permissive struct decoder.
// Bound nesting independently of the provider's response byte cap.
func validateQuotedJSON(raw []byte) error {
	if !utf8.Valid(raw) {
		return errors.New("quote proposal is not UTF-8")
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	var walk func(int) error
	walk = func(depth int) error {
		if depth > 64 {
			return errors.New("quote proposal nesting exceeds budget")
		}
		token, err := d.Token()
		if err != nil {
			return err
		}
		switch token {
		case json.Delim('{'):
			seen := map[string]bool{}
			for d.More() {
				keyToken, err := d.Token()
				if err != nil {
					return err
				}
				key, ok := keyToken.(string)
				if !ok || key != strings.ToLower(key) || seen[key] {
					return errors.New("quote proposal has duplicate or noncanonical keys")
				}
				seen[key] = true
				if err := walk(depth + 1); err != nil {
					return err
				}
			}
			end, err := d.Token()
			if err != nil || end != json.Delim('}') {
				return errors.New("invalid quote proposal object")
			}
		case json.Delim('['):
			for d.More() {
				if err := walk(depth + 1); err != nil {
					return err
				}
			}
			end, err := d.Token()
			if err != nil || end != json.Delim(']') {
				return errors.New("invalid quote proposal array")
			}
		}
		return nil
	}
	if err := walk(0); err != nil {
		return err
	}
	return rejectTrailingJSON(d)
}
