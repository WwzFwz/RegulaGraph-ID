// Mengaudit inventory akuisisi D01 dan membentuk manifest deterministik untuk M01/G01.
// Peran: mengikat queue, latest record, observation history, HTML provenance, dan blob PDF
// tanpa mengubah assertion portal menjadi identitas regulasi kanonis atau fakta hukum.
// Kontrak: setiap referenced artifact diverifikasi ukuran, SHA-256, envelope PDF, safe relative
// path, serta konsistensi receipt; output record JSONL diurutkan dan diberi content hash.
// Benchmark: hashing memakai worker terbatas dan streaming buffer; ukur bytes/detik, peak RSS,
// p50/p95 file audit, issue rate, dan coverage per portal pada corpus referensi.
// Target numerik required: configs/benchmark-targets.yaml; status REQUIRED_UNMEASURED.
// Status: audit inventory D01 aktif; klasifikasi text/scan/table dan canonical resolution
// menunggu M01/I01/K01 dan tidak diinferensikan dari metadata portal.
package sources

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
)

const inventorySchemaVersion = 1

type AuditOptions struct {
	Root            string
	Workers         int
	MaxIssueSamples int
}

type InventoryIssue struct {
	Code       string `json:"code"`
	Severity   string `json:"severity"`
	SourceURL  string `json:"source_url,omitempty"`
	RecordPath string `json:"record_path,omitempty"`
	Detail     string `json:"detail"`
}

type InventoryPDF struct {
	URL          string `json:"url"`
	FinalURL     string `json:"final_url,omitempty"`
	Kind         string `json:"kind"`
	SHA256       string `json:"sha256,omitempty"`
	Path         string `json:"path,omitempty"`
	Bytes        int64  `json:"bytes,omitempty"`
	ContentType  string `json:"content_type,omitempty"`
	ETag         string `json:"etag,omitempty"`
	LastModified string `json:"last_modified,omitempty"`
	Error        string `json:"error,omitempty"`
}

type InventoryRecord struct {
	RecordKey           string              `json:"record_key"`
	SourceURL           string              `json:"source_url"`
	FinalURL            string              `json:"final_url,omitempty"`
	Portal              string              `json:"portal"`
	FetchedAt           time.Time           `json:"fetched_at"`
	Status              string              `json:"status"`
	ParserVersion       string              `json:"metadata_parser_version"`
	HTMLSHA256          string              `json:"html_sha256,omitempty"`
	HTMLPath            string              `json:"html_path,omitempty"`
	Title               string              `json:"title,omitempty"`
	PortalFields        map[string][]string `json:"portal_fields,omitempty"`
	PDFs                []InventoryPDF      `json:"pdfs,omitempty"`
	DocumentURLs        []string            `json:"document_urls,omitempty"`
	MissingDocumentURLs []string            `json:"missing_document_urls,omitempty"`
	IdentityCandidate   string              `json:"identity_candidate,omitempty"`
	IdentityStatus      string              `json:"identity_status"`
	ObservationCount    int                 `json:"observation_count"`
	IntegrityStatus     string              `json:"integrity_status"`
}

type PortalAudit struct {
	Records       int   `json:"records"`
	Complete      int   `json:"complete"`
	Partial       int   `json:"partial"`
	Failed        int   `json:"failed"`
	PDFReferences int   `json:"pdf_references"`
	PDFBytes      int64 `json:"pdf_bytes"`
}

type InventoryManifest struct {
	SchemaVersion           int                    `json:"schema_version"`
	GeneratedAt             time.Time              `json:"generated_at"`
	InventoryID             string                 `json:"inventory_id"`
	RecordsPath             string                 `json:"records_path"`
	RecordsSHA256           string                 `json:"records_sha256"`
	ObservationsSHA256      string                 `json:"observations_sha256"`
	QueueSHA256             string                 `json:"queue_sha256"`
	BlobSetSHA256           string                 `json:"blob_set_sha256"`
	Records                 int                    `json:"records"`
	Observations            int                    `json:"observations"`
	UniquePDFs              int                    `json:"unique_pdfs"`
	UniquePDFBytes          int64                  `json:"unique_pdf_bytes"`
	ReferencedPDFs          int                    `json:"referenced_pdfs"`
	OrphanPDFs              int                    `json:"orphan_pdfs"`
	QueueURLs               int                    `json:"queue_urls"`
	QueueAcquired           int                    `json:"queue_acquired"`
	QueuePending            int                    `json:"queue_pending"`
	DocumentReferences      int                    `json:"document_references"`
	MissingDocumentRefs     int                    `json:"missing_document_references"`
	CandidateIdentityGroups int                    `json:"candidate_identity_groups"`
	CandidateCollisions     int                    `json:"candidate_identity_collisions"`
	MetadataCoverage        map[string]int         `json:"metadata_coverage"`
	StatusCounts            map[string]int         `json:"status_counts"`
	Portals                 map[string]PortalAudit `json:"portals"`
	IssueCounts             map[string]int         `json:"issue_counts"`
	IssueSamples            []InventoryIssue       `json:"issue_samples,omitempty"`
	IntegrityErrors         int                    `json:"integrity_errors"`
	IntegrityValid          bool                   `json:"integrity_valid"`
	FormatClassification    string                 `json:"format_classification"`
}

type InventoryAudit struct {
	Manifest InventoryManifest
	Records  []InventoryRecord
}

type issueCollector struct {
	max     int
	counts  map[string]int
	samples []InventoryIssue
	errors  int
}

func newIssueCollector(max int) *issueCollector {
	if max <= 0 {
		max = 100
	}
	return &issueCollector{max: max, counts: map[string]int{}}
}

func (c *issueCollector) add(issue InventoryIssue) {
	c.counts[issue.Code]++
	if issue.Severity == "error" {
		c.errors++
	}
	if len(c.samples) < c.max {
		c.samples = append(c.samples, issue)
	}
}

func AuditAcquisition(ctx context.Context, opts AuditOptions) (InventoryAudit, error) {
	if opts.Root == "" {
		return InventoryAudit{}, errors.New("audit root is required")
	}
	if opts.Workers <= 0 {
		opts.Workers = runtime.GOMAXPROCS(0)
	}
	if opts.Workers > 32 {
		opts.Workers = 32
	}
	root, err := os.OpenRoot(opts.Root)
	if err != nil {
		return InventoryAudit{}, fmt.Errorf("open audit root: %w", err)
	}
	defer root.Close()
	issues := newIssueCollector(opts.MaxIssueSamples)
	records, latestHashes, references, err := auditLatestRecords(ctx, root, issues)
	if err != nil {
		return InventoryAudit{}, err
	}
	observationCounts, matchingObservationHashes, observationTotal, observationHash, err := auditObservations(ctx, root, issues, references, latestHashes)
	if err != nil {
		return InventoryAudit{}, err
	}
	for i := range records {
		records[i].ObservationCount = observationCounts[records[i].SourceURL]
		if records[i].ObservationCount == 0 || matchingObservationHashes[records[i].SourceURL] != latestHashes[records[i].SourceURL] {
			issues.add(InventoryIssue{Code: "missing_matching_observation", Severity: "error", SourceURL: records[i].SourceURL, Detail: "latest record has no byte-identical immutable observation"})
			records[i].IntegrityStatus = "invalid"
		}
	}
	blobs, err := auditBlobs(ctx, root, opts.Workers, issues)
	if err != nil {
		return InventoryAudit{}, err
	}
	validateReferences(records, references, blobs, issues)
	acquired := make(map[string]struct{}, len(records))
	for _, record := range records {
		acquired[record.SourceURL] = struct{}{}
	}
	for i := range records {
		missing := make([]string, 0)
		for _, target := range records[i].DocumentURLs {
			if _, ok := acquired[target]; !ok {
				missing = append(missing, target)
			}
		}
		sort.Strings(missing)
		records[i].MissingDocumentURLs = missing
	}
	queueTotal, queueAcquired, queueHash, err := auditQueue(ctx, root, acquired, issues)
	if err != nil {
		return InventoryAudit{}, err
	}
	sort.Slice(records, func(i, j int) bool { return records[i].SourceURL < records[j].SourceURL })
	rows, rowsHash, err := marshalInventoryRows(records)
	if err != nil {
		return InventoryAudit{}, err
	}
	blobSetHash, uniqueBytes := hashBlobSet(blobs)
	manifest := summarizeInventory(records, blobs, references, observationTotal, queueTotal, queueAcquired, issues)
	manifest.GeneratedAt = time.Now().UTC()
	manifest.RecordsPath = "inventory.records.jsonl"
	manifest.RecordsSHA256 = rowsHash
	manifest.ObservationsSHA256 = observationHash
	manifest.QueueSHA256 = queueHash
	manifest.BlobSetSHA256 = blobSetHash
	manifest.UniquePDFBytes = uniqueBytes
	idSeed := fmt.Sprintf("schema=%d\nrecords=%s\nobservations=%s\nqueue=%s\nblobs=%s\n", inventorySchemaVersion, rowsHash, observationHash, queueHash, blobSetHash)
	manifest.InventoryID = digest([]byte(idSeed))
	return InventoryAudit{Manifest: manifest, Records: records}, writeInventory(opts.Root, manifest, rows)
}

func auditLatestRecords(ctx context.Context, root *os.Root, issues *issueCollector) ([]InventoryRecord, map[string]string, map[string][]int64, error) {
	entries, err := fs.ReadDir(root.FS(), "records")
	if err != nil {
		return nil, nil, nil, fmt.Errorf("read records: %w", err)
	}
	records := make([]InventoryRecord, 0, len(entries))
	latestHashes := map[string]string{}
	references := map[string][]int64{}
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return nil, nil, nil, err
		}
		if entry.Type()&os.ModeSymlink != 0 || entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			issues.add(InventoryIssue{Code: "unexpected_record_entry", Severity: "error", RecordPath: filepath.ToSlash(filepath.Join("records", entry.Name())), Detail: "record entry must be a regular JSON file"})
			continue
		}
		rel := filepath.ToSlash(filepath.Join("records", entry.Name()))
		var raw Record
		encoded, err := decodeJSONFile(root, rel, 16<<20, &raw)
		if err != nil {
			issues.add(InventoryIssue{Code: "malformed_record", Severity: "error", RecordPath: rel, Detail: err.Error()})
			continue
		}
		latestHashes[raw.SourceURL] = digest(encoded)
		row := inventoryRow(raw, strings.TrimSuffix(entry.Name(), ".json"))
		row.IntegrityStatus = "valid"
		if raw.SchemaVersion != inventorySchemaVersion {
			issues.add(InventoryIssue{Code: "unsupported_record_schema", Severity: "error", SourceURL: raw.SourceURL, RecordPath: rel, Detail: fmt.Sprintf("schema_version=%d", raw.SchemaVersion)})
			row.IntegrityStatus = "invalid"
		}
		if !validHash(row.RecordKey) || row.RecordKey != digest([]byte(raw.SourceURL)) {
			issues.add(InventoryIssue{Code: "record_key_mismatch", Severity: "error", SourceURL: raw.SourceURL, RecordPath: rel, Detail: "filename must be SHA-256 of source_url"})
			row.IntegrityStatus = "invalid"
		}
		parsed, parseErr := url.Parse(raw.SourceURL)
		if parseErr != nil || parsed.Scheme != "https" || parsed.Hostname() == "" || parsed.Hostname() != raw.Portal {
			issues.add(InventoryIssue{Code: "portal_url_mismatch", Severity: "error", SourceURL: raw.SourceURL, RecordPath: rel, Detail: "source URL must be HTTPS and hostname must match portal"})
			row.IntegrityStatus = "invalid"
		}
		if raw.Status != "complete" && raw.Status != "partial" && raw.Status != "failed" {
			issues.add(InventoryIssue{Code: "invalid_record_status", Severity: "error", SourceURL: raw.SourceURL, RecordPath: rel, Detail: raw.Status})
			row.IntegrityStatus = "invalid"
		}
		if !auditHTMLProvenance(ctx, root, raw, issues, rel) {
			row.IntegrityStatus = "invalid"
		}
		successful := 0
		for _, receipt := range raw.PDFs {
			if receipt.Error != "" {
				if receipt.Path != "" || receipt.SHA256 != "" || receipt.Bytes != 0 {
					issues.add(InventoryIssue{Code: "failed_receipt_has_artifact", Severity: "error", SourceURL: raw.SourceURL, RecordPath: rel, Detail: receipt.URL})
					row.IntegrityStatus = "invalid"
				}
				continue
			}
			successful++
			expectedPath := path.Join("blobs", receipt.SHA256+".pdf")
			if !validHash(receipt.SHA256) || receipt.Bytes <= 0 || receipt.Path != expectedPath || !safeRelativeKey(receipt.Path) {
				issues.add(InventoryIssue{Code: "invalid_pdf_receipt", Severity: "error", SourceURL: raw.SourceURL, RecordPath: rel, Detail: receipt.URL})
				row.IntegrityStatus = "invalid"
				continue
			}
			references[receipt.SHA256] = append(references[receipt.SHA256], receipt.Bytes)
		}
		if raw.Status == "complete" && (successful == 0 || successful != len(raw.PDFs)) {
			issues.add(InventoryIssue{Code: "inconsistent_complete_record", Severity: "error", SourceURL: raw.SourceURL, RecordPath: rel, Detail: "complete record must contain only successful PDF receipts"})
			row.IntegrityStatus = "invalid"
		}
		if !requiredPDFReceiptsPresent(raw) {
			issues.add(InventoryIssue{Code: "missing_required_pdf_receipt", Severity: "error", SourceURL: raw.SourceURL, RecordPath: rel, Detail: "metadata pdf_links are not fully represented by successful receipts"})
			row.IntegrityStatus = "invalid"
		}
		if raw.Status != "complete" {
			issues.add(InventoryIssue{Code: "source_acquisition_incomplete", Severity: "warning", SourceURL: raw.SourceURL, RecordPath: rel, Detail: raw.Error})
		}
		records = append(records, row)
	}
	return records, latestHashes, references, nil
}

func inventoryRow(record Record, key string) InventoryRecord {
	row := InventoryRecord{RecordKey: key, SourceURL: record.SourceURL, FinalURL: record.FinalURL, Portal: record.Portal,
		FetchedAt: record.FetchedAt, Status: record.Status, ParserVersion: record.ParserVersion, HTMLSHA256: record.HTMLSHA256, HTMLPath: record.HTMLPath, Title: record.Metadata.Title,
		PortalFields: record.Metadata.Fields, DocumentURLs: uniqueSorted(record.Metadata.DocumentURLs), IdentityStatus: "unverified"}
	for _, receipt := range record.PDFs {
		row.PDFs = append(row.PDFs, InventoryPDF{URL: receipt.URL, FinalURL: receipt.FinalURL, Kind: receipt.Kind, SHA256: receipt.SHA256,
			Path: receipt.Path, Bytes: receipt.Bytes, ContentType: receipt.ContentType, ETag: receipt.ETag, LastModified: receipt.LastModified, Error: receipt.Error})
	}
	typeValue := firstField(record.Metadata.Fields, "regulation_type", "regulation_abbreviation")
	number := firstField(record.Metadata.Fields, "number")
	year := firstField(record.Metadata.Fields, "year")
	issuer := firstField(record.Metadata.Fields, "issuer")
	if typeValue != "" && number != "" && year != "" {
		row.IdentityCandidate = normalizeIdentityPart(typeValue) + "/" + normalizeIdentityPart(number) + "/" + normalizeIdentityPart(year) + "/" + normalizeIdentityPart(issuer)
	}
	return row
}

func auditObservations(ctx context.Context, root *os.Root, issues *issueCollector, references map[string][]int64, latestHashes map[string]string) (map[string]int, map[string]string, int, string, error) {
	entries, err := fs.ReadDir(root.FS(), "observations")
	if err != nil {
		return nil, nil, 0, "", fmt.Errorf("read observations: %w", err)
	}
	counts := map[string]int{}
	matches := map[string]string{}
	setHasher := sha256.New()
	total := 0
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return nil, nil, total, "", err
		}
		rel := filepath.ToSlash(filepath.Join("observations", entry.Name()))
		if entry.Type()&os.ModeSymlink != 0 || entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			issues.add(InventoryIssue{Code: "unexpected_observation_entry", Severity: "error", RecordPath: rel, Detail: "observation entry must be a regular JSON file"})
			fmt.Fprintf(setHasher, "%s\tinvalid-entry\n", entry.Name())
			continue
		}
		var observation Record
		encoded, err := decodeJSONFile(root, rel, 16<<20, &observation)
		if err != nil {
			issues.add(InventoryIssue{Code: "malformed_observation", Severity: "error", RecordPath: rel, Detail: err.Error()})
			fmt.Fprintf(setHasher, "%s\tmalformed\n", entry.Name())
			continue
		}
		encodedHash := digest(encoded)
		fmt.Fprintf(setHasher, "%s\t%s\n", entry.Name(), encodedHash)
		if observation.SchemaVersion != inventorySchemaVersion || observation.SourceURL == "" {
			issues.add(InventoryIssue{Code: "invalid_observation", Severity: "error", RecordPath: rel, Detail: "unsupported schema or missing source_url"})
			continue
		}
		expectedSuffix := "-" + digest([]byte(observation.SourceURL)) + ".json"
		if !strings.HasSuffix(entry.Name(), expectedSuffix) {
			issues.add(InventoryIssue{Code: "observation_key_mismatch", Severity: "error", SourceURL: observation.SourceURL, RecordPath: rel, Detail: "filename must end with SHA-256 of source_url"})
		}
		if !validObservationRecord(ctx, root, observation, rel, issues, references) {
			// The global integrity error is sufficient; latest row validity is assessed separately.
		}
		counts[observation.SourceURL]++
		if encodedHash == latestHashes[observation.SourceURL] {
			matches[observation.SourceURL] = encodedHash
		}
		total++
	}
	return counts, matches, total, hex.EncodeToString(setHasher.Sum(nil)), nil
}

type blobAudit struct {
	SHA256 string
	Path   string
	Bytes  int64
	Valid  bool
}

func auditBlobs(ctx context.Context, root *os.Root, workers int, issues *issueCollector) (map[string]blobAudit, error) {
	entries, err := fs.ReadDir(root.FS(), "blobs")
	if err != nil {
		return nil, fmt.Errorf("read blobs: %w", err)
	}
	type result struct {
		blob blobAudit
		err  error
	}
	jobs := make(chan os.DirEntry)
	results := make(chan result)
	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			buffer := make([]byte, 1<<20)
			for entry := range jobs {
				blob, hashErr := hashPDF(ctx, root, entry, buffer)
				select {
				case results <- result{blob: blob, err: hashErr}:
				case <-ctx.Done():
					return
				}
			}
		}()
	}
	go func() {
		defer close(jobs)
		for _, entry := range entries {
			select {
			case jobs <- entry:
			case <-ctx.Done():
				return
			}
		}
	}()
	go func() { wg.Wait(); close(results) }()
	blobs := make(map[string]blobAudit, len(entries))
	for item := range results {
		if item.err != nil {
			issues.add(InventoryIssue{Code: "invalid_pdf_blob", Severity: "error", RecordPath: item.blob.Path, Detail: item.err.Error()})
			continue
		}
		blobs[item.blob.SHA256] = item.blob
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return blobs, nil
}

func hashPDF(ctx context.Context, root *os.Root, entry os.DirEntry, buffer []byte) (blobAudit, error) {
	rel := filepath.ToSlash(filepath.Join("blobs", entry.Name()))
	result := blobAudit{Path: rel}
	if entry.Type()&os.ModeSymlink != 0 || entry.IsDir() || strings.ToLower(filepath.Ext(entry.Name())) != ".pdf" {
		return result, errors.New("blob entry must be a regular .pdf file")
	}
	expected := strings.TrimSuffix(entry.Name(), filepath.Ext(entry.Name()))
	result.SHA256 = expected
	if !validHash(expected) {
		return result, errors.New("blob filename is not a lowercase SHA-256")
	}
	f, err := root.Open(rel)
	if err != nil {
		return result, err
	}
	defer f.Close()
	h := sha256.New()
	written, err := copyWithContext(ctx, h, f, buffer)
	if err != nil {
		return result, err
	}
	result.Bytes = written
	actual := hex.EncodeToString(h.Sum(nil))
	if actual != expected {
		return result, fmt.Errorf("content hash %s does not match filename", actual)
	}
	head := make([]byte, 1024)
	if _, err = f.Seek(0, io.SeekStart); err != nil {
		return result, err
	}
	n, _ := f.Read(head)
	if !bytes.HasPrefix(bytes.TrimSpace(head[:n]), []byte("%PDF-")) {
		return result, errors.New("missing PDF header")
	}
	offset := written - 4096
	if offset < 0 {
		offset = 0
	}
	if _, err = f.Seek(offset, io.SeekStart); err != nil {
		return result, err
	}
	tail, err := io.ReadAll(io.LimitReader(f, 4096))
	if err != nil {
		return result, err
	}
	if !bytes.Contains(tail, []byte("%%EOF")) {
		return result, errors.New("missing PDF EOF marker")
	}
	result.Valid = true
	return result, nil
}

func copyWithContext(ctx context.Context, dst io.Writer, src io.Reader, buffer []byte) (int64, error) {
	var total int64
	for {
		if err := ctx.Err(); err != nil {
			return total, err
		}
		n, readErr := src.Read(buffer)
		if n > 0 {
			written, writeErr := dst.Write(buffer[:n])
			total += int64(written)
			if writeErr != nil {
				return total, writeErr
			}
			if written != n {
				return total, io.ErrShortWrite
			}
		}
		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				return total, nil
			}
			return total, readErr
		}
	}
}

func validateReferences(records []InventoryRecord, references map[string][]int64, blobs map[string]blobAudit, issues *issueCollector) {
	invalid := map[string]struct{}{}
	for hash, sizes := range references {
		blob, ok := blobs[hash]
		if !ok || !blob.Valid {
			issues.add(InventoryIssue{Code: "missing_referenced_blob", Severity: "error", RecordPath: path.Join("blobs", hash+".pdf"), Detail: "receipt references absent or invalid PDF blob"})
			invalid[hash] = struct{}{}
			continue
		}
		for _, size := range sizes {
			if size != blob.Bytes {
				issues.add(InventoryIssue{Code: "receipt_size_mismatch", Severity: "error", RecordPath: blob.Path, Detail: fmt.Sprintf("receipt=%d actual=%d", size, blob.Bytes)})
				invalid[hash] = struct{}{}
				break
			}
		}
	}
	for hash, blob := range blobs {
		if _, ok := references[hash]; !ok {
			issues.add(InventoryIssue{Code: "orphan_pdf_blob", Severity: "warning", RecordPath: blob.Path, Detail: "valid blob has no latest-record receipt"})
		}
	}
	if len(invalid) == 0 {
		return
	}
	for i := range records {
		for _, pdf := range records[i].PDFs {
			if _, bad := invalid[pdf.SHA256]; bad {
				records[i].IntegrityStatus = "invalid"
				break
			}
		}
	}
}

func auditQueue(ctx context.Context, root *os.Root, acquired map[string]struct{}, issues *issueCollector) (int, int, string, error) {
	f, err := root.Open("queue.txt")
	if errors.Is(err, os.ErrNotExist) {
		issues.add(InventoryIssue{Code: "missing_queue", Severity: "warning", Detail: "queue.txt is absent"})
		return 0, 0, digest(nil), nil
	}
	if err != nil {
		return 0, 0, "", err
	}
	defer f.Close()
	seen := map[string]struct{}{}
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 4096), 1<<20)
	matched := 0
	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return 0, 0, "", err
		}
		line := strings.TrimSpace(strings.TrimPrefix(scanner.Text(), "\ufeff"))
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if _, duplicate := seen[line]; duplicate {
			issues.add(InventoryIssue{Code: "duplicate_queue_url", Severity: "warning", SourceURL: line, Detail: "duplicate queue entry ignored"})
			continue
		}
		seen[line] = struct{}{}
		if _, ok := acquired[line]; ok {
			matched++
		}
	}
	if err := scanner.Err(); err != nil {
		return 0, 0, "", err
	}
	urls := make([]string, 0, len(seen))
	for rawURL := range seen {
		urls = append(urls, rawURL)
	}
	sort.Strings(urls)
	return len(seen), matched, digest([]byte(strings.Join(urls, "\n") + "\n")), nil
}

func summarizeInventory(records []InventoryRecord, blobs map[string]blobAudit, references map[string][]int64, observations, queueTotal, queueAcquired int, issues *issueCollector) InventoryManifest {
	manifest := InventoryManifest{SchemaVersion: inventorySchemaVersion, Records: len(records), Observations: observations,
		UniquePDFs: len(blobs), ReferencedPDFs: len(references), QueueURLs: queueTotal, QueueAcquired: queueAcquired,
		QueuePending: queueTotal - queueAcquired, MetadataCoverage: map[string]int{}, StatusCounts: map[string]int{},
		Portals: map[string]PortalAudit{}, IssueCounts: issues.counts, IssueSamples: issues.samples,
		IntegrityErrors: issues.errors, IntegrityValid: issues.errors == 0, FormatClassification: "unknown_until_m01"}
	for hash := range blobs {
		if _, ok := references[hash]; !ok {
			manifest.OrphanPDFs++
		}
	}
	identityCounts := map[string]int{}
	for _, record := range records {
		manifest.StatusCounts[record.Status]++
		portal := manifest.Portals[record.Portal]
		portal.Records++
		switch record.Status {
		case "complete":
			portal.Complete++
		case "partial":
			portal.Partial++
		case "failed":
			portal.Failed++
		}
		for _, pdf := range record.PDFs {
			if pdf.Error == "" {
				portal.PDFReferences++
				portal.PDFBytes += pdf.Bytes
			}
		}
		manifest.Portals[record.Portal] = portal
		if record.Title != "" {
			manifest.MetadataCoverage["title"]++
		}
		for _, field := range []string{"issuer", "regulation_type", "number", "year", "portal_status", "effective_date", "enactment_date", "promulgation_date", "publication_source", "language"} {
			if len(record.PortalFields[field]) > 0 {
				manifest.MetadataCoverage[field]++
			}
		}
		manifest.DocumentReferences += len(record.DocumentURLs)
		manifest.MissingDocumentRefs += len(record.MissingDocumentURLs)
		if record.IdentityCandidate != "" {
			identityCounts[record.IdentityCandidate]++
		}
	}
	manifest.CandidateIdentityGroups = len(identityCounts)
	for _, count := range identityCounts {
		if count > 1 {
			manifest.CandidateCollisions++
		}
	}
	return manifest
}

func marshalInventoryRows(records []InventoryRecord) ([]byte, string, error) {
	var output bytes.Buffer
	encoder := json.NewEncoder(&output)
	encoder.SetEscapeHTML(false)
	for _, record := range records {
		if err := encoder.Encode(record); err != nil {
			return nil, "", err
		}
	}
	raw := output.Bytes()
	return raw, digest(raw), nil
}

func hashBlobSet(blobs map[string]blobAudit) (string, int64) {
	keys := make([]string, 0, len(blobs))
	for key := range blobs {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	h := sha256.New()
	var total int64
	for _, key := range keys {
		blob := blobs[key]
		fmt.Fprintf(h, "%s\t%d\n", key, blob.Bytes)
		total += blob.Bytes
	}
	return hex.EncodeToString(h.Sum(nil)), total
}

func writeInventory(root string, manifest InventoryManifest, rows []byte) error {
	if err := writeAtomic(filepath.Join(root, manifest.RecordsPath), rows); err != nil {
		return fmt.Errorf("write inventory records: %w", err)
	}
	raw, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	if err := writeAtomic(filepath.Join(root, "inventory.json"), raw); err != nil {
		return fmt.Errorf("write inventory manifest: %w", err)
	}
	return nil
}

func auditHTMLProvenance(ctx context.Context, root *os.Root, record Record, issues *issueCollector, recordPath string) bool {
	if record.HTMLPath == "" && record.HTMLSHA256 == "" {
		for _, receipt := range record.PDFs {
			if receipt.Error == "" && receipt.URL == record.SourceURL {
				return true
			}
		}
		if failedBeforeResponse(record) {
			return true
		}
		issues.add(InventoryIssue{Code: "missing_html_provenance", Severity: "error", SourceURL: record.SourceURL, RecordPath: recordPath, Detail: "detail-page record requires html_path and html_sha256"})
		return false
	}
	if record.HTMLPath == "" || record.HTMLSHA256 == "" {
		issues.add(InventoryIssue{Code: "incomplete_html_reference", Severity: "error", SourceURL: record.SourceURL, RecordPath: recordPath, Detail: "html_path and html_sha256 must be present together"})
		return false
	}
	return auditContentFile(ctx, root, record.HTMLPath, record.HTMLSHA256, 8<<20, 0, issues, record.SourceURL, "html")
}

func requiredPDFReceiptsPresent(record Record) bool {
	if record.Status != "complete" {
		return true
	}
	successful := map[string]struct{}{}
	for _, receipt := range record.PDFs {
		if receipt.Error == "" {
			successful[receipt.URL] = struct{}{}
		}
	}
	for _, link := range record.Metadata.PDFs {
		if _, ok := successful[link.URL]; !ok {
			return false
		}
	}
	return true
}

func validObservationRecord(ctx context.Context, root *os.Root, record Record, recordPath string, issues *issueCollector, references map[string][]int64) bool {
	valid := true
	if record.Status != "complete" && record.Status != "partial" && record.Status != "failed" {
		issues.add(InventoryIssue{Code: "invalid_observation_status", Severity: "error", SourceURL: record.SourceURL, RecordPath: recordPath, Detail: record.Status})
		valid = false
	}
	parsed, err := url.Parse(record.SourceURL)
	if err != nil || parsed.Scheme != "https" || parsed.Hostname() == "" || parsed.Hostname() != record.Portal {
		issues.add(InventoryIssue{Code: "observation_portal_url_mismatch", Severity: "error", SourceURL: record.SourceURL, RecordPath: recordPath, Detail: "source URL must be HTTPS and hostname must match portal"})
		valid = false
	}
	if !auditHTMLProvenance(ctx, root, record, issues, recordPath) {
		valid = false
	}
	successful := 0
	for _, receipt := range record.PDFs {
		if receipt.Error != "" {
			if receipt.Path != "" || receipt.SHA256 != "" || receipt.Bytes != 0 {
				issues.add(InventoryIssue{Code: "historical_failed_receipt_has_artifact", Severity: "error", SourceURL: record.SourceURL, RecordPath: recordPath, Detail: receipt.URL})
				valid = false
			}
			continue
		}
		successful++
		expectedPath := path.Join("blobs", receipt.SHA256+".pdf")
		if !validHash(receipt.SHA256) || receipt.Bytes <= 0 || receipt.Path != expectedPath || !safeRelativeKey(receipt.Path) {
			issues.add(InventoryIssue{Code: "invalid_historical_pdf_receipt", Severity: "error", SourceURL: record.SourceURL, RecordPath: recordPath, Detail: receipt.URL})
			valid = false
			continue
		}
		references[receipt.SHA256] = append(references[receipt.SHA256], receipt.Bytes)
	}
	if record.Status == "complete" && (successful == 0 || successful != len(record.PDFs) || !requiredPDFReceiptsPresent(record)) {
		issues.add(InventoryIssue{Code: "inconsistent_historical_record", Severity: "error", SourceURL: record.SourceURL, RecordPath: recordPath, Detail: "complete observation must contain every required successful PDF receipt"})
		valid = false
	}
	return valid
}

func failedBeforeResponse(record Record) bool {
	return record.Status == "failed" && record.Error != "" && len(record.PDFs) == 0 &&
		record.Metadata.Title == "" && len(record.Metadata.Fields) == 0 && len(record.Metadata.PDFs) == 0 &&
		len(record.Metadata.RelatedPDFs) == 0 && len(record.Metadata.DocumentURLs) == 0
}

func auditContentFile(ctx context.Context, root *os.Root, key, expected string, maxBytes, expectedBytes int64, issues *issueCollector, sourceURL, kind string) bool {
	if !safeRelativeKey(key) || !validHash(expected) {
		issues.add(InventoryIssue{Code: "invalid_" + kind + "_reference", Severity: "error", SourceURL: sourceURL, RecordPath: key, Detail: "unsafe path or invalid SHA-256"})
		return false
	}
	info, err := root.Stat(key)
	if err != nil || !info.Mode().IsRegular() {
		detail := "artifact is not a confined regular file"
		if err != nil {
			detail = err.Error()
		}
		issues.add(InventoryIssue{Code: "missing_" + kind + "_artifact", Severity: "error", SourceURL: sourceURL, RecordPath: key, Detail: detail})
		return false
	}
	f, err := root.Open(key)
	if err != nil {
		issues.add(InventoryIssue{Code: "missing_" + kind + "_artifact", Severity: "error", SourceURL: sourceURL, RecordPath: key, Detail: err.Error()})
		return false
	}
	defer f.Close()
	h := sha256.New()
	reader := io.Reader(f)
	if maxBytes > 0 {
		reader = io.LimitReader(f, maxBytes+1)
	}
	n, err := copyWithContext(ctx, h, reader, make([]byte, 64<<10))
	if err != nil || (maxBytes > 0 && n > maxBytes) || (expectedBytes > 0 && n != expectedBytes) || hex.EncodeToString(h.Sum(nil)) != expected {
		issues.add(InventoryIssue{Code: "corrupt_" + kind + "_artifact", Severity: "error", SourceURL: sourceURL, RecordPath: key, Detail: "content hash or byte size mismatch"})
		return false
	}
	return true
}

func decodeJSONFile(root *os.Root, key string, maxBytes int64, target any) ([]byte, error) {
	if !safeRelativeKey(key) {
		return nil, errors.New("unsafe JSON path")
	}
	info, err := root.Stat(key)
	if err != nil || !info.Mode().IsRegular() {
		return nil, errors.New("JSON entry is not a confined regular file")
	}
	f, err := root.Open(key)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, maxBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(raw)) > maxBytes {
		return nil, errors.New("JSON exceeds size limit")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return nil, err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return nil, errors.New("multiple JSON values")
		}
		return nil, err
	}
	return raw, nil
}

func safeRelativeKey(key string) bool {
	return key != "" && !strings.Contains(key, "\\") && !strings.Contains(key, ":") && !strings.HasPrefix(key, "/") && key != "." && key != ".." && !strings.HasPrefix(key, "../") && path.Clean(key) == key
}

func firstField(fields map[string][]string, names ...string) string {
	for _, name := range names {
		for _, value := range fields[name] {
			if value = strings.TrimSpace(value); value != "" {
				return value
			}
		}
	}
	return ""
}

func normalizeIdentityPart(value string) string {
	return strings.Join(strings.Fields(strings.ToLower(value)), "-")
}

func uniqueSorted(values []string) []string {
	seen := map[string]struct{}{}
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			seen[value] = struct{}{}
		}
	}
	result := make([]string, 0, len(seen))
	for value := range seen {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}
