// Loads the shared, versioned graph ontology and validates extraction records against it.
//
// The loader accepts bounded JSONC with leading comments, rejects unknown fields and duplicate or
// dangling vocabulary entries, and hashes the exact source bytes for cross-runtime pinning. The
// compiled maps are immutable after construction. Validation is linear in mentions, assertions,
// and qualifiers; semantic quality and ontology coverage still require the configured gold gates.
package domain

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"

	pb "regulagraph.local/server/gen/regulagraph/v1"
)

const maximumOntologyBytes = 1 << 20

type ontologySource struct {
	SchemaVersion   uint32                    `json:"schema_version"`
	OntologyVersion string                    `json:"ontology_version"`
	EntityTypes     []string                  `json:"entity_types"`
	Qualifiers      []ontologyQualifierSource `json:"qualifiers"`
	Predicates      []ontologyPredicateSource `json:"predicates"`
}

type ontologyQualifierSource struct {
	ID         string   `json:"id"`
	ValueKinds []string `json:"value_kinds"`
}

type ontologyPredicateSource struct {
	ID           string   `json:"id"`
	SubjectTypes []string `json:"subject_types"`
	ObjectTypes  []string `json:"object_types"`
	QualifierIDs []string `json:"qualifier_ids"`
	Origins      []string `json:"origins"`
	AllowSelf    bool     `json:"allow_self"`
}

type qualifierValueKind uint8

const (
	qualifierValueEntity qualifierValueKind = iota + 1
	qualifierValueLiteral
	qualifierValueDate
	qualifierValueNumber
)

type ontologyPredicate struct {
	subjectTypes map[string]struct{}
	objectTypes  map[string]struct{}
	qualifiers   map[string]struct{}
	origins      map[pb.AssertionOrigin]struct{}
	allowSelf    bool
}

// Ontology is an immutable compiled vocabulary loaded once during process startup.
type Ontology struct {
	version     string
	contentHash string
	entityTypes map[string]struct{}
	qualifiers  map[string]map[qualifierValueKind]struct{}
	predicates  map[string]ontologyPredicate
}

// ParseOntologyJSONC parses and compiles an ontology while hashing the exact input bytes.
func ParseOntologyJSONC(raw []byte) (*Ontology, error) {
	if len(raw) == 0 || len(raw) > maximumOntologyBytes || !utf8.Valid(raw) {
		return nil, errors.New("ontology source must be valid UTF-8 and at most 1 MiB")
	}
	payload, err := trimLeadingJSONCComments(raw)
	if err != nil {
		return nil, err
	}
	if err := validateOntologyJSONShape(payload); err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	var source ontologySource
	if err = decoder.Decode(&source); err != nil {
		return nil, fmt.Errorf("decode ontology: %w", err)
	}
	if err = requireJSONEOF(decoder); err != nil {
		return nil, err
	}
	ontology, err := compileOntology(source)
	if err != nil {
		return nil, err
	}
	digest := sha256.Sum256(raw)
	ontology.contentHash = hex.EncodeToString(digest[:])
	return ontology, nil
}

// validateOntologyJSONShape enforces exact case-sensitive keys, presence, and JSON value types.
// Struct decoding alone would accept case variants and silently default missing boolean fields.
func validateOntologyJSONShape(payload []byte) error {
	root, err := exactJSONObject(payload, []string{"schema_version", "ontology_version", "entity_types", "qualifiers", "predicates"})
	if err != nil {
		return err
	}
	for _, name := range []string{"qualifiers", "predicates", "entity_types"} {
		if len(root[name]) == 0 || root[name][0] != '[' {
			return fmt.Errorf("ontology %q must be an array", name)
		}
	}
	if len(root["ontology_version"]) == 0 || root["ontology_version"][0] != '"' {
		return errors.New("ontology_version must be a string")
	}
	for _, group := range []struct {
		name   string
		fields []string
		arrays []string
	}{
		{"qualifiers", []string{"id", "value_kinds"}, []string{"value_kinds"}},
		{"predicates", []string{"id", "subject_types", "object_types", "qualifier_ids", "origins", "allow_self"},
			[]string{"subject_types", "object_types", "qualifier_ids", "origins"}},
	} {
		var entries []json.RawMessage
		if err := json.Unmarshal(root[group.name], &entries); err != nil {
			return fmt.Errorf("decode ontology %s: %w", group.name, err)
		}
		for index, entry := range entries {
			object, err := exactJSONObject(entry, group.fields)
			if err != nil {
				return fmt.Errorf("ontology %s[%d]: %w", group.name, index, err)
			}
			if len(object["id"]) == 0 || object["id"][0] != '"' {
				return fmt.Errorf("ontology %s[%d].id must be a string", group.name, index)
			}
			for _, name := range group.arrays {
				if len(object[name]) == 0 || object[name][0] != '[' {
					return fmt.Errorf("ontology %s[%d].%s must be an array", group.name, index, name)
				}
			}
			if group.name == "predicates" && string(object["allow_self"]) != "true" && string(object["allow_self"]) != "false" {
				return fmt.Errorf("ontology predicates[%d].allow_self must be a boolean", index)
			}
		}
	}
	return nil
}

func exactJSONObject(raw []byte, fields []string) (map[string]json.RawMessage, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	start, err := decoder.Token()
	if err != nil || start != json.Delim('{') {
		return nil, errors.New("ontology entry must be a JSON object")
	}
	values := make(map[string]json.RawMessage, len(fields))
	allowed := make(map[string]struct{}, len(fields))
	for _, field := range fields {
		allowed[field] = struct{}{}
	}
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return nil, fmt.Errorf("decode ontology key: %w", err)
		}
		name, ok := token.(string)
		if !ok {
			return nil, errors.New("ontology key must be a string")
		}
		if _, ok := allowed[name]; !ok {
			return nil, fmt.Errorf("unknown ontology field %q", name)
		}
		if _, duplicate := values[name]; duplicate {
			return nil, fmt.Errorf("duplicate ontology field %q", name)
		}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return nil, fmt.Errorf("decode ontology field %q: %w", name, err)
		}
		values[name] = value
	}
	if _, err := decoder.Token(); err != nil {
		return nil, fmt.Errorf("decode ontology object end: %w", err)
	}
	if err := requireJSONEOF(decoder); err != nil {
		return nil, err
	}
	if len(values) != len(fields) {
		return nil, errors.New("ontology entry omits required fields")
	}
	return values, nil
}

func trimLeadingJSONCComments(raw []byte) ([]byte, error) {
	if bytes.HasPrefix(raw, []byte{0xef, 0xbb, 0xbf}) {
		return nil, errors.New("ontology source must be UTF-8 without BOM")
	}
	scanner := bufio.NewScanner(bytes.NewReader(raw))
	scanner.Buffer(make([]byte, 4096), maximumOntologyBytes)
	var payload bytes.Buffer
	started := false
	for scanner.Scan() {
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)
		if !started && (trimmed == "" || strings.HasPrefix(trimmed, "//")) {
			continue
		}
		started = true
		payload.WriteString(line)
		payload.WriteByte('\n')
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan ontology: %w", err)
	}
	if !started {
		return nil, errors.New("ontology JSON payload is missing")
	}
	return payload.Bytes(), nil
}

func requireJSONEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("ontology has multiple JSON values")
		}
		return fmt.Errorf("decode trailing ontology data: %w", err)
	}
	return nil
}

func compileOntology(source ontologySource) (*Ontology, error) {
	if source.SchemaVersion != 1 || !validOntologyVersion(source.OntologyVersion) ||
		len(source.EntityTypes) == 0 || len(source.Qualifiers) == 0 || len(source.Predicates) == 0 {
		return nil, errors.New("ontology requires schema version 1, version, entity types, qualifiers, and predicates")
	}
	ontology := &Ontology{
		version: source.OntologyVersion, entityTypes: make(map[string]struct{}, len(source.EntityTypes)),
		qualifiers: make(map[string]map[qualifierValueKind]struct{}, len(source.Qualifiers)),
		predicates: make(map[string]ontologyPredicate, len(source.Predicates)),
	}
	for _, entityType := range source.EntityTypes {
		if !validVocabularyID(entityType) {
			return nil, fmt.Errorf("invalid ontology entity type %q", entityType)
		}
		if _, duplicate := ontology.entityTypes[entityType]; duplicate {
			return nil, fmt.Errorf("duplicate ontology entity type %q", entityType)
		}
		ontology.entityTypes[entityType] = struct{}{}
	}
	for _, qualifier := range source.Qualifiers {
		if !validVocabularyID(qualifier.ID) || len(qualifier.ValueKinds) == 0 {
			return nil, fmt.Errorf("invalid ontology qualifier %q", qualifier.ID)
		}
		if _, duplicate := ontology.qualifiers[qualifier.ID]; duplicate {
			return nil, fmt.Errorf("duplicate ontology qualifier %q", qualifier.ID)
		}
		kinds := make(map[qualifierValueKind]struct{}, len(qualifier.ValueKinds))
		for _, value := range qualifier.ValueKinds {
			kind, ok := parseQualifierValueKind(value)
			if !ok {
				return nil, fmt.Errorf("qualifier %q has unknown value kind %q", qualifier.ID, value)
			}
			if _, duplicate := kinds[kind]; duplicate {
				return nil, fmt.Errorf("qualifier %q repeats value kind %q", qualifier.ID, value)
			}
			kinds[kind] = struct{}{}
		}
		ontology.qualifiers[qualifier.ID] = kinds
	}
	for _, predicate := range source.Predicates {
		if !validVocabularyID(predicate.ID) || len(predicate.SubjectTypes) == 0 || len(predicate.ObjectTypes) == 0 || len(predicate.Origins) == 0 {
			return nil, fmt.Errorf("invalid ontology predicate %q", predicate.ID)
		}
		if _, duplicate := ontology.predicates[predicate.ID]; duplicate {
			return nil, fmt.Errorf("duplicate ontology predicate %q", predicate.ID)
		}
		compiled := ontologyPredicate{
			subjectTypes: make(map[string]struct{}, len(predicate.SubjectTypes)),
			objectTypes:  make(map[string]struct{}, len(predicate.ObjectTypes)),
			qualifiers:   make(map[string]struct{}, len(predicate.QualifierIDs)),
			origins:      make(map[pb.AssertionOrigin]struct{}, len(predicate.Origins)), allowSelf: predicate.AllowSelf,
		}
		if err := addKnownIDs(compiled.subjectTypes, predicate.SubjectTypes, ontology.entityTypes, "subject type", predicate.ID); err != nil {
			return nil, err
		}
		if err := addKnownIDs(compiled.objectTypes, predicate.ObjectTypes, ontology.entityTypes, "object type", predicate.ID); err != nil {
			return nil, err
		}
		qualifierIDs := make(map[string]struct{}, len(ontology.qualifiers))
		for id := range ontology.qualifiers {
			qualifierIDs[id] = struct{}{}
		}
		if err := addKnownIDs(compiled.qualifiers, predicate.QualifierIDs, qualifierIDs, "qualifier", predicate.ID); err != nil {
			return nil, err
		}
		for _, origin := range predicate.Origins {
			value, ok := parseAssertionOrigin(origin)
			if !ok {
				return nil, fmt.Errorf("predicate %q has unknown origin %q", predicate.ID, origin)
			}
			if _, duplicate := compiled.origins[value]; duplicate {
				return nil, fmt.Errorf("predicate %q repeats origin %q", predicate.ID, origin)
			}
			compiled.origins[value] = struct{}{}
		}
		ontology.predicates[predicate.ID] = compiled
	}
	return ontology, nil
}

func addKnownIDs(target map[string]struct{}, values []string, known map[string]struct{}, field, predicate string) error {
	for _, value := range values {
		if _, exists := known[value]; !exists {
			return fmt.Errorf("predicate %q references unknown %s %q", predicate, field, value)
		}
		if _, duplicate := target[value]; duplicate {
			return fmt.Errorf("predicate %q repeats %s %q", predicate, field, value)
		}
		target[value] = struct{}{}
	}
	return nil
}

func validVocabularyID(value string) bool {
	if len(value) == 0 || len(value) > 64 || value[0] < 'a' || value[0] > 'z' {
		return false
	}
	for _, character := range []byte(value[1:]) {
		if !(character >= 'a' && character <= 'z' || character >= '0' && character <= '9' || character == '_') {
			return false
		}
	}
	return true
}

func validOntologyVersion(value string) bool {
	if len(value) == 0 || len(value) > 128 {
		return false
	}
	for _, character := range []byte(value) {
		if !(character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' ||
			character >= '0' && character <= '9' || strings.ContainsRune("._:-", rune(character))) {
			return false
		}
	}
	return true
}

func parseQualifierValueKind(value string) (qualifierValueKind, bool) {
	switch value {
	case "entity":
		return qualifierValueEntity, true
	case "literal":
		return qualifierValueLiteral, true
	case "date":
		return qualifierValueDate, true
	case "number":
		return qualifierValueNumber, true
	default:
		return 0, false
	}
}

func parseAssertionOrigin(value string) (pb.AssertionOrigin, bool) {
	switch value {
	case "explicit":
		return pb.AssertionOrigin_ASSERTION_ORIGIN_EXPLICIT, true
	case "inferred":
		return pb.AssertionOrigin_ASSERTION_ORIGIN_INFERRED, true
	default:
		return pb.AssertionOrigin_ASSERTION_ORIGIN_UNSPECIFIED, false
	}
}

// Version returns the immutable ontology identity carried by graph artifacts.
func (ontology *Ontology) Version() string {
	if ontology == nil {
		return ""
	}
	return ontology.version
}

// ContentHash returns the SHA-256 of the exact JSONC bytes used to compile the ontology.
func (ontology *Ontology) ContentHash() *pb.ContentHash {
	if ontology == nil || ontology.contentHash == "" {
		return nil
	}
	return &pb.ContentHash{Sha256: ontology.contentHash}
}

// ValidateExtractionOntology checks mention types and typed assertion vocabulary in one pass.
func (ontology *Ontology) ValidateExtractionOntology(batch *pb.ExtractionBatch) error {
	if ontology == nil || batch == nil || batch.OntologyVersion != ontology.version {
		return errors.New("extraction ontology is missing or has a different version")
	}
	return ontology.ValidateExtractionRecords(batch.OntologyVersion, batch.Mentions, batch.Assertions)
}

// ValidateExtractionRecords applies the same ontology gate before a complete batch is assembled.
func (ontology *Ontology) ValidateExtractionRecords(
	version string,
	batchMentions []*pb.Mention,
	batchAssertions []*pb.RelationAssertion,
) error {
	if ontology == nil || version != ontology.version {
		return errors.New("extraction ontology is missing or has a different version")
	}
	mentions := make(map[string]string, len(batchMentions))
	for _, mention := range batchMentions {
		if mention == nil || mention.GetMeta() == nil {
			return errors.New("ontology validation received an incomplete mention")
		}
		id := mention.GetMeta().GetRecordId()
		if id == "" {
			return errors.New("ontology validation received a mention without ID")
		}
		if _, duplicate := mentions[id]; duplicate {
			return fmt.Errorf("ontology validation received duplicate mention %q", id)
		}
		if _, known := ontology.entityTypes[mention.GetCandidateType()]; !known {
			return fmt.Errorf("mention %q has unknown ontology type %q", id, mention.GetCandidateType())
		}
		mentions[id] = mention.CandidateType
	}
	for _, assertion := range batchAssertions {
		if assertion == nil || assertion.GetMeta() == nil {
			return errors.New("ontology validation received an incomplete assertion")
		}
		id := assertion.GetMeta().GetRecordId()
		if id == "" || assertion.OntologyVersion != version {
			return fmt.Errorf("assertion %q has no ID or differs from ontology version %q", id, version)
		}
		predicate, known := ontology.predicates[assertion.PredicateId]
		if !known {
			return fmt.Errorf("assertion %q has unknown predicate %q", id, assertion.PredicateId)
		}
		subjectType, subjectKnown := mentions[assertion.SubjectId]
		objectType, objectKnown := mentions[assertion.ObjectId]
		if !subjectKnown || !objectKnown {
			return fmt.Errorf("assertion %q has endpoints outside its extraction mentions", id)
		}
		if _, allowed := predicate.subjectTypes[subjectType]; !allowed {
			return fmt.Errorf("assertion %q rejects subject type %q for predicate %q", id, subjectType, assertion.PredicateId)
		}
		if _, allowed := predicate.objectTypes[objectType]; !allowed {
			return fmt.Errorf("assertion %q rejects object type %q for predicate %q", id, objectType, assertion.PredicateId)
		}
		if !predicate.allowSelf && assertion.SubjectId == assertion.ObjectId {
			return fmt.Errorf("assertion %q uses a forbidden self edge", id)
		}
		if _, allowed := predicate.origins[assertion.Origin]; !allowed {
			return fmt.Errorf("assertion %q has origin disallowed by predicate %q", id, assertion.PredicateId)
		}
		for _, qualifier := range assertion.Qualifiers {
			if qualifier == nil {
				return fmt.Errorf("assertion %q has a nil qualifier", id)
			}
			if _, allowed := predicate.qualifiers[qualifier.PredicateId]; !allowed {
				return fmt.Errorf("assertion %q uses qualifier %q outside predicate %q", id, qualifier.PredicateId, assertion.PredicateId)
			}
			allowedKinds := ontology.qualifiers[qualifier.PredicateId]
			kind, ok := qualifierKind(qualifier)
			if !ok {
				return fmt.Errorf("assertion %q has empty qualifier %q", id, qualifier.PredicateId)
			}
			if _, allowed := allowedKinds[kind]; !allowed {
				return fmt.Errorf("assertion %q uses the wrong value kind for qualifier %q", id, qualifier.PredicateId)
			}
		}
	}
	return nil
}

func qualifierKind(qualifier *pb.Qualifier) (qualifierValueKind, bool) {
	if qualifier == nil {
		return 0, false
	}
	switch qualifier.GetValue().(type) {
	case *pb.Qualifier_MentionId, *pb.Qualifier_CanonicalId:
		return qualifierValueEntity, true
	case *pb.Qualifier_Literal:
		return qualifierValueLiteral, true
	case *pb.Qualifier_Date:
		return qualifierValueDate, true
	case *pb.Qualifier_Number:
		return qualifierValueNumber, true
	default:
		return 0, false
	}
}
