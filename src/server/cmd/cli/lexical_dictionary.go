// Operator command for the registry-owned vocabulary-to-dictionary handoff.
// Reads exact SHA-pinned UTF-8 vocabulary produced by the Rust population pass,
// invokes indexing preparation, and writes a binary C01 ArtifactRef to a new file.
// No migrations, source authorization or publication are inferred from this
// command. Configuration credentials stay out of output; measure startup and
// allocation/I/O separately under configs/benchmark-targets.yaml.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"google.golang.org/protobuf/proto"
	"regulagraph.local/server/internal/adapters/postgres"
	"regulagraph.local/server/internal/adapters/storage"
	"regulagraph.local/server/internal/indexing"
	"regulagraph.local/server/internal/retrieval/query"
)

func readLexicalVocabulary(path, pin string) ([]string, error) {
	hash, err := hex.DecodeString(pin)
	if err != nil || len(hash) != 32 || strings.ToLower(pin) != pin {
		return nil, errors.New("lowercase vocabulary SHA-256 required")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() == 0 || info.Size() > 16<<20 {
		return nil, errors.New("vocabulary must be a regular file within 16 MiB")
	}
	raw, err := io.ReadAll(io.LimitReader(file, (16<<20)+1))
	if err != nil {
		return nil, err
	}
	if len(raw) == 0 || len(raw) > 16<<20 || fmt.Sprintf("%x", sha256.Sum256(raw)) != pin {
		return nil, errors.New("vocabulary byte/hash mismatch")
	}
	if raw[len(raw)-1] != '\n' {
		return nil, errors.New("vocabulary requires LF-terminated UTF-8 lines")
	}
	terms := strings.Split(string(raw[:len(raw)-1]), "\n")
	if len(terms) > 49999 {
		return nil, errors.New("vocabulary exceeds dictionary wire capacity")
	}
	for i, term := range terms {
		parsed, e := query.AnalyzeLexicalQuery(term)
		if e != nil || len(parsed) != 1 || parsed[0] != term || len(term) > 256 || i > 0 && terms[i-1] >= term {
			return nil, errors.New("vocabulary is not canonical sorted analyzer output")
		}
	}
	return terms, nil
}

func runLexicalDictionary(ctx context.Context, args []string, out, errOut io.Writer) int {
	fs := flag.NewFlagSet("prepare-dictionary", flag.ContinueOnError)
	fs.SetOutput(errOut)
	vocabulary := fs.String("vocabulary", "", "Rust vocabulary file, sorted LF-delimited UTF-8")
	pin := fs.String("vocabulary-sha256", "", "Exact SHA-256 of the vocabulary bytes")
	corpus := fs.String("corpus", "", "Authorized operator corpus ID")
	output := fs.String("output", "", "New binary ArtifactRef output file")
	timeout := fs.Duration("timeout", 2*time.Minute, "Overall preparation deadline, up to five minutes")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	dsn, root := os.Getenv("REGULAGRAPH_POSTGRES_DSN"), os.Getenv("REGULAGRAPH_ARTIFACTS_DIR")
	if fs.NArg() != 0 || *corpus == "" || *output == "" || dsn == "" || root == "" || *timeout <= 0 || *timeout > 5*time.Minute {
		fmt.Fprintln(errOut, "invalid preparation arguments/configuration")
		return 2
	}
	terms, err := readLexicalVocabulary(*vocabulary, *pin)
	if err != nil {
		fmt.Fprintln(errOut, "invalid vocabulary file/pin")
		return 2
	}
	file, err := os.OpenFile(*output, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		fmt.Fprintln(errOut, "cannot create new reference output")
		return 1
	}
	defer file.Close()
	bounded, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()
	repo, err := postgres.Open(bounded, postgres.Config{DSN: dsn, MaxConnections: 2, HealthTimeout: 5 * time.Second})
	if err != nil {
		fmt.Fprintln(errOut, "dictionary database unavailable")
		return 1
	}
	defer repo.Close()
	store, err := storage.NewFileStore(root)
	if err != nil {
		fmt.Fprintln(errOut, "artifact storage unavailable")
		return 1
	}
	defer store.Close()
	ref, err := indexing.PrepareLexicalDictionary(bounded, repo, store, *corpus, terms)
	if err != nil {
		fmt.Fprintln(errOut, "dictionary preparation failed; retry the same pinned vocabulary")
		return 1
	}
	raw, err := proto.Marshal(ref)
	if err == nil {
		_, err = file.Write(raw)
	}
	if err == nil {
		err = file.Sync()
	}
	if err != nil {
		fmt.Fprintln(errOut, "reference output failed; allocation may already be durable")
		return 1
	}
	if _, err = fmt.Fprintf(out, "prepared %s; publication pending\n", ref.ArtifactId); err != nil {
		return 1
	}
	return 0
}
