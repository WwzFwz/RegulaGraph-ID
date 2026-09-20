// Converts verified acquisition receipts into production wire provenance for ingestion.
//
// The handoff binds each portal observation to immutable PDF bytes by SHA-256 while preserving
// portal metadata as sourced assertions. It does not assign canonical regulation identities or
// interpret dates/status as legal truth. Output ordering and IDs are deterministic for replay.
// Conversion sorts receipts and field values for deterministic replay, costing O(R log R + sum(V log V));
// benchmark source throughput separately.
package sources

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"
	pb "regulagraph.local/server/gen/regulagraph/v1"
)

var handoffIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)

type IngestionHandoff struct {
	Sources      []*pb.SourceLocator
	Observations []*pb.SourceObservation
}

// BuildIngestionHandoff projects one complete acquisition record into source locators and
// observations. Blob files remain relative to the collector/artifact root named by receipt.Path.
func BuildIngestionHandoff(corpusID string, record Record) (IngestionHandoff, error) {
	if !handoffIDPattern.MatchString(corpusID) {
		return IngestionHandoff{}, errors.New("valid corpus ID is required")
	}
	if record.SchemaVersion != 1 || record.ParserVersion == "" || record.Status != "complete" || record.SourceURL == "" {
		return IngestionHandoff{}, errors.New("complete schema-v1 acquisition record is required")
	}
	portalID, err := productionPortalID(record)
	if err != nil {
		return IngestionHandoff{}, err
	}
	if record.FinalURL != "" {
		if err = validateURL(record.FinalURL); err != nil {
			return IngestionHandoff{}, fmt.Errorf("invalid resolved detail URL: %w", err)
		}
	}
	metadata, err := canonicalPortalMetadata(record)
	if err != nil {
		return IngestionHandoff{}, err
	}
	receipts := append([]PDFReceipt(nil), record.PDFs...)
	sort.Slice(receipts, func(i, j int) bool {
		return receiptOrderKey(receipts[i], record.FetchedAt) < receiptOrderKey(receipts[j], record.FetchedAt)
	})
	result := IngestionHandoff{}
	type blobDescriptor struct {
		storageKey string
		mediaType  string
		byteSize   uint64
	}
	seenBlob := map[string]blobDescriptor{}
	seenObservation := map[string]bool{}
	for _, receipt := range receipts {
		if err = validateHandoffReceipt(receipt); err != nil {
			return IngestionHandoff{}, err
		}
		blobID := "source-blob:" + receipt.SHA256
		artifact := &pb.ArtifactRef{
			ArtifactId:  "source-artifact:" + receipt.SHA256,
			ContentHash: &pb.ContentHash{Sha256: receipt.SHA256},
			StorageKey:  receipt.Path,
			MediaType:   "application/pdf",
			ByteSize:    uint64(receipt.Bytes), SchemaVersion: 1,
		}
		descriptor := blobDescriptor{
			storageKey: receipt.Path,
			mediaType:  artifact.MediaType,
			byteSize:   artifact.ByteSize,
		}
		if previous, exists := seenBlob[blobID]; exists && previous != descriptor {
			return IngestionHandoff{}, fmt.Errorf("conflicting descriptors for source content %s", receipt.SHA256)
		} else if !exists {
			result.Sources = append(result.Sources, &pb.SourceLocator{
				PortalId: portalID, Locator: &pb.SourceLocator_Blob{Blob: artifact},
			})
			seenBlob[blobID] = descriptor
		}
		fetchedAt := receipt.FetchedAt.UTC()
		if fetchedAt.IsZero() {
			fetchedAt = record.FetchedAt.UTC()
		}
		if fetchedAt.IsZero() {
			return IngestionHandoff{}, errors.New("source observation requires fetch time")
		}
		fetchedTimestamp := timestamppb.New(fetchedAt)
		if err = fetchedTimestamp.CheckValid(); err != nil {
			return IngestionHandoff{}, fmt.Errorf("invalid source observation fetch time: %w", err)
		}
		observationKey := strings.Join([]string{portalID, record.SourceURL, receipt.URL, receipt.SHA256, fetchedAt.Format(time.RFC3339Nano)}, "\x00")
		observationID := "source-observation:" + digest([]byte(observationKey))
		if seenObservation[observationID] {
			return IngestionHandoff{}, errors.New("duplicate source observation")
		}
		seenObservation[observationID] = true
		values := cloneNamedValues(metadata)
		values = appendTextValue(values, "document_kind", receipt.Kind)
		values = appendTextValue(values, "document_label", receipt.Label)
		values = appendTextValue(values, "artifact_url", receipt.URL)
		values = appendTextValue(values, "artifact_final_url", receipt.FinalURL)
		metadataHash, err := namedValuesHash(values)
		if err != nil {
			return IngestionHandoff{}, err
		}
		resolvedURL := record.FinalURL
		result.Observations = append(result.Observations, &pb.SourceObservation{
			Meta:     &pb.RecordMeta{SchemaVersion: 1, CorpusId: corpusID, RecordId: observationID},
			PortalId: portalID, DetailUrl: record.SourceURL, ResolvedUrl: resolvedURL,
			FetchedAt: fetchedTimestamp, Status: pb.ObservationStatus_OBSERVATION_STATUS_COMPLETE,
			Etag: optionalText(receipt.ETag), LastModified: optionalText(receipt.LastModified),
			MetadataHash: &pb.ContentHash{Sha256: metadataHash}, SourceBlobId: &blobID,
			PortalMetadata: values,
		})
	}
	if len(result.Sources) == 0 || len(result.Observations) == 0 {
		return IngestionHandoff{}, errors.New("acquisition record contains no verified PDF receipts")
	}
	return result, nil
}

func receiptOrderKey(receipt PDFReceipt, fallback time.Time) string {
	fetchedAt := receipt.FetchedAt.UTC()
	if fetchedAt.IsZero() {
		fetchedAt = fallback.UTC()
	}
	parts := []string{
		receipt.SHA256, receipt.URL, receipt.FinalURL, receipt.Kind, receipt.Label, receipt.Path,
		strconv.FormatInt(receipt.Bytes, 10), receipt.ContentType, receipt.ETag, receipt.LastModified,
		fetchedAt.Format(time.RFC3339Nano), strconv.FormatInt(receipt.DurationMS, 10), receipt.Error,
	}
	var key strings.Builder
	for _, part := range parts {
		key.WriteString(strconv.Itoa(len(part)))
		key.WriteByte(':')
		key.WriteString(part)
	}
	return key.String()
}

func validateHandoffReceipt(receipt PDFReceipt) error {
	if receipt.Error != "" || !validHash(receipt.SHA256) || receipt.Bytes <= 0 || receipt.Path == "" ||
		receipt.Path != "blobs/"+receipt.SHA256+".pdf" || receipt.URL == "" {
		return fmt.Errorf("invalid verified PDF receipt for %q", receipt.URL)
	}
	if err := validateURL(receipt.URL); err != nil {
		return fmt.Errorf("invalid receipt URL: %w", err)
	}
	if receipt.FinalURL != "" {
		if err := validateURL(receipt.FinalURL); err != nil {
			return fmt.Errorf("invalid final receipt URL: %w", err)
		}
	}
	mediaType := strings.ToLower(strings.TrimSpace(strings.Split(receipt.ContentType, ";")[0]))
	if mediaType != "" && mediaType != "application/pdf" {
		return fmt.Errorf("receipt media type is not PDF: %s", receipt.ContentType)
	}
	return nil
}

func productionPortalID(record Record) (string, error) {
	if err := validateURL(record.SourceURL); err != nil {
		return "", fmt.Errorf("invalid source detail URL: %w", err)
	}
	parsed, _ := url.Parse(record.SourceURL)
	sourceHost := strings.ToLower(parsed.Hostname())
	host := strings.ToLower(strings.TrimSpace(record.Portal))
	if host == "" {
		host = sourceHost
	} else if host != sourceHost {
		return "", errors.New("record portal differs from source detail URL")
	}
	switch host {
	case "peraturan.bpk.go.id":
		return "bpk", nil
	case "jdih.komdigi.go.id":
		return "komdigi", nil
	case "jdihn.go.id", "www.jdihn.go.id":
		return "jdihn", nil
	default:
		return "", fmt.Errorf("unsupported source portal: %s", host)
	}
}

func canonicalPortalMetadata(record Record) ([]*pb.NamedValue, error) {
	fields := make([]string, 0, len(record.Metadata.Fields))
	for field := range record.Metadata.Fields {
		fields = append(fields, field)
	}
	sort.Strings(fields)
	values := make([]*pb.NamedValue, 0, len(fields)+3)
	if title := strings.TrimSpace(record.Metadata.Title); title != "" {
		values = append(values, textValue("page_title", title))
	}
	values = append(values, textValue("metadata_parser_version", record.ParserVersion))
	for _, field := range fields {
		items := make([]string, 0, len(record.Metadata.Fields[field]))
		for _, item := range record.Metadata.Fields[field] {
			if item = strings.TrimSpace(item); item != "" {
				items = append(items, item)
			}
		}
		sort.Strings(items)
		previous := ""
		for _, item := range items {
			if item == previous {
				continue
			}
			values = append(values, textValue(field, item))
			previous = item
		}
	}
	return values, nil
}

// namedValuesHash fingerprints exactly the ordered assertions emitted on SourceObservation.
// Canonicalization happens before this function, so equivalent input order and spacing replay to
// the same hash while any observable metadata change produces a different fingerprint.
func namedValuesHash(values []*pb.NamedValue) (string, error) {
	payload := make([]struct {
		Name string `json:"name"`
		Text string `json:"text"`
	}, 0, len(values))
	for _, value := range values {
		payload = append(payload, struct {
			Name string `json:"name"`
			Text string `json:"text"`
		}{Name: value.GetName(), Text: value.GetText()})
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("encode portal metadata fingerprint: %w", err)
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

func textValue(name, value string) *pb.NamedValue {
	return &pb.NamedValue{Name: name, Value: &pb.NamedValue_Text{Text: value}}
}

func appendTextValue(values []*pb.NamedValue, name, value string) []*pb.NamedValue {
	if value = strings.TrimSpace(value); value != "" {
		return append(values, textValue(name, value))
	}
	return values
}

func cloneNamedValues(values []*pb.NamedValue) []*pb.NamedValue {
	cloned := make([]*pb.NamedValue, 0, len(values))
	for _, value := range values {
		cloned = append(cloned, textValue(value.Name, value.GetText()))
	}
	return cloned
}

func optionalText(value string) *string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	return &value
}
