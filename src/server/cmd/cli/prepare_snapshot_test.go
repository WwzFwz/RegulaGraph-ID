// Verifies snapshot selection errors before I/O and C01 export interoperability.
// Exported snapshot/ref bytes decode with production validators, facts hash stays
// pinned, and existing exports cannot be overwritten. Snapshot authority is
// covered separately by indexing's real PostgreSQL test.
package main

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/domain"
	"regulagraph.local/server/internal/indexing"
)

func TestSnapshotSelectionAndExport(t *testing.T) {
	for _, values := range [][]string{nil, {"job"}, {"job=a", "job=b"}, {"job=a", "other=a"}, {"=artifact"}, {"job=artifact space"}} {
		if _, err := parseSnapshotSelections("corpus:test", values); err == nil {
			t.Fatal("invalid selection accepted", values)
		}
	}
	selection, err := parseSnapshotSelections("corpus:test", []string{"job:a=artifact:a", "job:b=artifact:b"})
	if err != nil || len(selection) != 2 {
		t.Fatal(err)
	}
	facts := []byte(`{"schema_version":1,"corpus_id":"corpus:test","snapshot_id":"snapshot:test","representation_generation":"generation:test","documents":1,"chunks":1,"canonical_entities":0,"graph_edges":0}`)
	ref := &pb.ArtifactRef{SchemaVersion: 1, ArtifactId: "artifact:a", ContentHash: &pb.ContentHash{Sha256: strings.Repeat("a", 64)}, ByteSize: 1, MediaType: "application/x-protobuf; message=regulagraph.v1.DocumentBatch", StorageKey: "objects/a"}
	result := &indexing.InitialSourceSnapshot{ManifestBytes: facts, Snapshot: &pb.SnapshotRef{CorpusId: "corpus:test", SnapshotId: "snapshot:test", Sequence: 1, RepresentationGeneration: "generation:test", ManifestHash: &pb.ContentHash{Sha256: fmt.Sprintf("%x", sha256.Sum256(facts))}}, Sources: []indexing.InitialIndexSource{{SourceJobID: "job:a", DocumentBatch: ref}}}
	dir := t.TempDir()
	if err = writeSnapshotPreparation(dir, result); err != nil {
		t.Fatal(err)
	}
	for name, message := range map[string]proto.Message{"snapshot.pb": new(pb.SnapshotRef), "source-001.pb": new(pb.ArtifactRef)} {
		raw, e := os.ReadFile(filepath.Join(dir, name))
		if e != nil {
			t.Fatal(e)
		}
		if e = domain.DecodeWire(raw, message, domain.DefaultWireLimits); e != nil {
			t.Fatal(e)
		}
	}
	raw, err := os.ReadFile(filepath.Join(dir, "corpus-facts.json"))
	if err != nil || !bytes.Equal(raw, facts) {
		t.Fatal("facts bytes changed", err)
	}
	if err = writeSnapshotPreparation(dir, result); err == nil {
		t.Fatal("existing output overwritten")
	}
	if _, err = os.Stat(filepath.Join(dir, "sources.json")); err != nil {
		t.Fatal("completion manifest missing", err)
	}
	snapshot, sources, err := readPreparedSnapshot(dir)
	if err != nil || !proto.Equal(snapshot, result.Snapshot) || len(sources) != 1 || sources[0].SourceJobID != "job:a" || sources[0].DocumentBatch.ArtifactId != ref.ArtifactId {
		t.Fatal("export cannot feed INDEX preparation", err)
	}
	for _, invalid := range []string{`[]`, `[{"source_job_id":"job:a","artifact_id":"artifact:a","unknown":true}]`, `[{"source_job_id":"job:a","artifact_id":"artifact:a"},{"source_job_id":"job:a","artifact_id":"artifact:b"}]`, `[] {}`} {
		if err = os.WriteFile(filepath.Join(dir, "sources.json"), []byte(invalid), 0600); err != nil {
			t.Fatal(err)
		}
		if _, _, err = readPreparedSnapshot(dir); err == nil {
			t.Fatal("invalid source export accepted", invalid)
		}
	}
	if _, err = readPreparationFile(filepath.Join(dir, "snapshot.pb"), 1); err == nil {
		t.Fatal("oversized input accepted")
	}
}
