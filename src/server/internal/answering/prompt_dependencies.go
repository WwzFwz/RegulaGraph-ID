// Projects missing dependency identifiers into short request-local handles for
// the model prompt. Every input position remains represented with its namespace;
// canonical IDs remain in ContextBundle/Answer and the projection audit hash.
// No evidence text, version, citation ID or missing-dependency count is pruned.
// Measure full-prompt tokens and latency, not only packed context tokens, against
// configs/benchmark-targets.yaml; token reduction is not a quality acceptance.
package answering

import (
	"fmt"
	"strings"
)

type promptDependency struct {
	Handle string `json:"handle"`
	Kind   string `json:"kind"`
}

// Opaque suffixes carry storage identity, not readable legal context. Namespace
// remains exact; IDs without a namespace use the explicit unknown category.
// Ordinals preserve order and multiplicity without alias collisions. This map is
// local to one generation; it never authorizes a claim or a source citation.
func projectPromptDependencies(canonical, selectedEvidence []string) []promptDependency {
	result := make([]promptDependency, len(canonical))
	reserved := make(map[string]bool, len(selectedEvidence))
	for _, id := range selectedEvidence {
		reserved[id] = true
	}
	ordinal := 0
	for i, id := range canonical {
		kind, _, namespaced := strings.Cut(id, ":")
		if !namespaced || kind == "" {
			kind = "unspecified"
		}
		var handle string
		for {
			ordinal++
			handle = fmt.Sprintf("missing:%d", ordinal)
			if !reserved[handle] {
				break
			}
		}
		result[i] = promptDependency{Handle: handle, Kind: kind}
	}
	return result
}
