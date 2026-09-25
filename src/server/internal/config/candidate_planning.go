// Loads a versioned, exact-byte-pinned candidate-planning policy catalog for job submission.
// The Go scheduler consumes the typed policies and persists their semantic fingerprints in
// IngestionRequest; RESOLVE checks those durable hashes before reading registry candidates.
// Loading occurs explicitly at startup, never during import or per mention. Bound config bytes,
// corpus count, and lookup budgets; measure candidate coverage and queue/lookup p95/p99 against
// configs/benchmark-targets.yaml, which remains REQUIRED_UNMEASURED.
package config

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"

	"regulagraph.local/server/internal/domain"
)

const maximumCandidatePolicyFileBytes = 1 << 20

var candidatePolicyCorpusIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,255}$`)

type candidatePolicyCatalog struct {
	SchemaVersion int                             `json:"schema_version"`
	Corpora       map[string]candidatePolicyEntry `json:"corpora"`
}

type candidatePolicyEntry struct {
	ScopesByType                map[string][]string `json:"scopes_by_type"`
	IncludeSourceRegulationType map[string]bool     `json:"include_source_regulation_type"`
	MaximumMentions             int                 `json:"maximum_mentions"`
	MaximumScopesPerMention     int                 `json:"maximum_scopes_per_mention"`
	MaximumTotalScopes          int                 `json:"maximum_total_scopes"`
}

// LoadCandidatePlanningPolicies accepts only a regular JSON file whose exact bytes match the
// operator-provided SHA-256. Unknown fields, trailing values, unconfigured corpora, invalid
// scope/type combinations, and unbounded limits fail before any job can be submitted.
func LoadCandidatePlanningPolicies(path, expectedSHA256 string) (map[string]domain.CandidatePlanningPolicy, error) {
	if path == "" || !lowerSHA256.MatchString(expectedSHA256) {
		return nil, errors.New("candidate policy path and lowercase SHA-256 are required")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open candidate policy catalog: %w", err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > maximumCandidatePolicyFileBytes {
		return nil, fmt.Errorf("candidate policy catalog must be a regular file of 1..%d bytes", maximumCandidatePolicyFileBytes)
	}
	raw, err := io.ReadAll(io.LimitReader(file, maximumCandidatePolicyFileBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read candidate policy catalog: %w", err)
	}
	if len(raw) > maximumCandidatePolicyFileBytes {
		return nil, errors.New("candidate policy catalog exceeded byte budget during read")
	}
	digest := sha256.Sum256(raw)
	if hex.EncodeToString(digest[:]) != expectedSHA256 {
		return nil, errors.New("candidate policy catalog bytes differ from pinned SHA-256")
	}
	if err := validateCandidatePolicyJSONShape(raw); err != nil {
		return nil, fmt.Errorf("candidate policy catalog shape: %w", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var catalog candidatePolicyCatalog
	if err := decoder.Decode(&catalog); err != nil {
		return nil, fmt.Errorf("decode candidate policy catalog: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return nil, errors.New("candidate policy catalog has trailing JSON")
	}
	if catalog.SchemaVersion != 1 || len(catalog.Corpora) == 0 || len(catalog.Corpora) > 1024 {
		return nil, errors.New("candidate policy catalog needs schema version 1 and 1..1024 corpora")
	}
	policies := make(map[string]domain.CandidatePlanningPolicy, len(catalog.Corpora))
	for corpusID, entry := range catalog.Corpora {
		if !candidatePolicyCorpusIDPattern.MatchString(corpusID) {
			return nil, fmt.Errorf("invalid candidate policy corpus %q", corpusID)
		}
		policy := domain.CandidatePlanningPolicy{ScopesByType: entry.ScopesByType,
			IncludeSourceRegulationType: entry.IncludeSourceRegulationType,
			MaximumMentions:             entry.MaximumMentions, MaximumScopesPerMention: entry.MaximumScopesPerMention,
			MaximumTotalScopes: entry.MaximumTotalScopes}
		if _, err := policy.Fingerprint(); err != nil {
			return nil, fmt.Errorf("candidate policy corpus %q: %w", corpusID, err)
		}
		policies[corpusID] = policy
	}
	return policies, nil
}

// validateCandidatePolicyJSONShape keeps reviewable JSON exact: duplicate/case-variant keys
// cannot be silently accepted by the standard struct decoder, whose matching is permissive.
func validateCandidatePolicyJSONShape(raw []byte) error {
	if err := rejectDuplicateCandidateJSONKeys(raw); err != nil {
		return err
	}
	root, err := candidatePolicyObject(raw,
		[]string{"schema_version", "corpora"}, nil)
	if err != nil {
		return err
	}
	if len(bytes.TrimSpace(root["corpora"])) == 0 || bytes.TrimSpace(root["corpora"])[0] != '{' {
		return errors.New("candidate policy corpora must be an object")
	}
	var entries map[string]json.RawMessage
	if err := json.Unmarshal(root["corpora"], &entries); err != nil {
		return err
	}
	for corpusID, entry := range entries {
		fields, err := candidatePolicyObject(entry,
			[]string{"scopes_by_type", "maximum_mentions", "maximum_scopes_per_mention", "maximum_total_scopes"},
			[]string{"include_source_regulation_type"})
		if err != nil {
			return fmt.Errorf("corpus %q: %w", corpusID, err)
		}
		if len(bytes.TrimSpace(fields["scopes_by_type"])) == 0 ||
			bytes.TrimSpace(fields["scopes_by_type"])[0] != '{' {
			return fmt.Errorf("corpus %q scopes_by_type must be an object", corpusID)
		}
		if sourceTypes, present := fields["include_source_regulation_type"]; present {
			if trimmed := bytes.TrimSpace(sourceTypes); len(trimmed) == 0 || trimmed[0] != '{' {
				return fmt.Errorf("corpus %q include_source_regulation_type must be an object", corpusID)
			}
			var flags map[string]json.RawMessage
			if err := json.Unmarshal(sourceTypes, &flags); err != nil {
				return err
			}
			for entityType, flag := range flags {
				if string(flag) != "true" && string(flag) != "false" {
					return fmt.Errorf("corpus %q source type %q needs a boolean", corpusID, entityType)
				}
			}
		}
	}
	return nil
}

func candidatePolicyObject(raw []byte, required, optional []string) (map[string]json.RawMessage, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil || fields == nil {
		return nil, errors.New("candidate policy entry must be an object")
	}
	allowed := make(map[string]bool, len(required)+len(optional))
	for _, key := range required {
		allowed[key] = true
		if _, ok := fields[key]; !ok {
			return nil, fmt.Errorf("candidate policy requires %q", key)
		}
	}
	for _, key := range optional {
		allowed[key] = true
	}
	for key := range fields {
		if !allowed[key] {
			return nil, fmt.Errorf("unknown candidate policy field %q", key)
		}
	}
	return fields, nil
}

func rejectDuplicateCandidateJSONKeys(raw []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	var scan func() error
	scan = func() error {
		token, err := decoder.Token()
		if err != nil {
			return err
		}
		delimiter, ok := token.(json.Delim)
		if !ok {
			return nil
		}
		switch delimiter {
		case '{':
			seen := map[string]bool{}
			for decoder.More() {
				keyToken, err := decoder.Token()
				if err != nil {
					return err
				}
				key, ok := keyToken.(string)
				if !ok || seen[key] {
					return errors.New("duplicate or invalid candidate policy JSON key")
				}
				seen[key] = true
				if err := scan(); err != nil {
					return err
				}
			}
			_, err := decoder.Token()
			return err
		case '[':
			for decoder.More() {
				if err := scan(); err != nil {
					return err
				}
			}
			_, err := decoder.Token()
			return err
		default:
			return errors.New("unexpected candidate policy JSON delimiter")
		}
	}
	if err := scan(); err != nil {
		return err
	}
	if _, err := decoder.Token(); err != io.EOF {
		return errors.New("candidate policy catalog has trailing JSON")
	}
	return nil
}
