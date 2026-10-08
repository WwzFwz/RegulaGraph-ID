// Allocates append-only BM25 term IDs for one corpus and analyzer in PostgreSQL.
// INDEX calls this once per bounded vocabulary batch and pins the returned revision;
// query readers load an immutable revision, never allocate terms per query token.
// A transaction serializes allocation on the dictionary row and records an operation
// digest for idempotent replay. Measure p95/p99, contention, pool wait, and RSS against
// configs/benchmark-targets.yaml; required release targets remain unmeasured.
// Serialization/deadlock rollback uses at most four attempts under caller
// cancellation; semantic conflicts and unknown commit outcomes are not retried.
package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"sort"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"regulagraph.local/server/internal/domain"
)

const maxLexicalAllocationTerms = 10_000

// LexicalTerm carries the immutable mapping visible at a dictionary revision.
type LexicalTerm = domain.LexicalTerm

// AllocateLexicalTerms returns IDs in caller order and the resulting revision.
// An identical operation key replays its historical revision even after later writes.
func (r *Repository) AllocateLexicalTerms(ctx context.Context, corpusID, analyzerID, operationKey string,
	expectedRevision uint64, terms []string) ([]LexicalTerm, uint64, error) {
	return retryDictionaryAllocation(ctx, func() ([]LexicalTerm, uint64, error) {
		return r.allocateLexicalTerms(ctx, corpusID, analyzerID, operationKey, expectedRevision, terms)
	})
}

func retryDictionaryAllocation(ctx context.Context, operation func() ([]LexicalTerm, uint64, error)) ([]LexicalTerm, uint64, error) {
	if ctx == nil {
		return nil, 0, errors.New("dictionary context required")
	}
	for attempt := 0; ; attempt++ {
		if err := ctx.Err(); err != nil {
			return nil, 0, err
		}
		mapping, revision, err := operation()
		var transactionError *pgconn.PgError
		if err == nil || attempt == 3 || !errors.As(err, &transactionError) || (transactionError.Code != "40001" && transactionError.Code != "40P01") {
			return mapping, revision, err
		}
		timer := time.NewTimer(time.Duration(10<<attempt) * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, 0, ctx.Err()
		case <-timer.C:
		}
	}
}

func (r *Repository) allocateLexicalTerms(ctx context.Context, corpusID, analyzerID, operationKey string,
	expectedRevision uint64, terms []string) ([]LexicalTerm, uint64, error) {
	ordered, digest, err := validateLexicalTerms(corpusID, analyzerID, operationKey, expectedRevision, terms)
	if err != nil {
		return nil, 0, err
	}
	if r == nil || r.pool == nil {
		return nil, 0, errors.New("postgres repository is closed")
	}
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return nil, 0, fmt.Errorf("begin dictionary allocation: %w", err)
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `INSERT INTO corpus_state(corpus_id) VALUES ($1) ON CONFLICT DO NOTHING`, corpusID); err != nil {
		return nil, 0, fmt.Errorf("ensure dictionary corpus: %w", err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO lexical_dictionary_state(corpus_id,analyzer_id)
		VALUES ($1,$2) ON CONFLICT DO NOTHING`, corpusID, analyzerID); err != nil {
		return nil, 0, fmt.Errorf("ensure dictionary state: %w", err)
	}
	var current, nextID int64
	if err = tx.QueryRow(ctx, `SELECT revision,next_term_id FROM lexical_dictionary_state
		WHERE corpus_id=$1 AND analyzer_id=$2 FOR UPDATE`, corpusID, analyzerID).Scan(&current, &nextID); err != nil {
		return nil, 0, fmt.Errorf("lock dictionary state: %w", err)
	}
	var oldHash string
	var oldCount int
	var oldRevision int64
	err = tx.QueryRow(ctx, `SELECT terms_hash,term_count,revision FROM lexical_dictionary_operations
		WHERE corpus_id=$1 AND analyzer_id=$2 AND operation_key=$3`, corpusID, analyzerID, operationKey).
		Scan(&oldHash, &oldCount, &oldRevision)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, 0, fmt.Errorf("read dictionary operation: %w", err)
	}
	replay := err == nil
	if replay && (oldHash != digest || oldCount != len(ordered)) {
		return nil, 0, fmt.Errorf("dictionary operation reused with different terms: %w", ErrConflict)
	}
	if replay && (oldRevision < 1 || oldRevision > current) {
		return nil, 0, fmt.Errorf("dictionary operation revision invalid: %w", domain.ErrPersistentIntegrity)
	}
	if !replay && expectedRevision != 0 && expectedRevision != uint64(current) {
		return nil, 0, fmt.Errorf("dictionary revision changed: %w", ErrConflict)
	}

	rows, err := tx.Query(ctx, `SELECT term,term_id,from_revision FROM lexical_dictionary_terms
		WHERE corpus_id=$1 AND analyzer_id=$2 AND term=ANY($3::text[])`, corpusID, analyzerID, ordered)
	if err != nil {
		return nil, 0, fmt.Errorf("read allocated terms: %w", err)
	}
	mapping := make(map[string]LexicalTerm, len(ordered))
	for rows.Next() {
		var term string
		var id, fromRevision int64
		if err = rows.Scan(&term, &id, &fromRevision); err != nil {
			break
		}
		if id < 1 || id > math.MaxUint32 || fromRevision < 2 || fromRevision > current ||
			(replay && fromRevision > oldRevision) {
			err = fmt.Errorf("dictionary term lineage invalid: %w", domain.ErrPersistentIntegrity)
			break
		}
		mapping[term] = LexicalTerm{Term: term, ID: uint32(id)}
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return nil, 0, fmt.Errorf("read dictionary mapping: %w", err)
	}
	if replay && len(mapping) != len(ordered) {
		return nil, 0, fmt.Errorf("dictionary replay mapping missing: %w", domain.ErrPersistentIntegrity)
	}
	if !replay && len(mapping) != len(ordered) {
		newTerms := make([]string, 0, len(ordered)-len(mapping))
		for _, term := range ordered {
			if _, exists := mapping[term]; !exists {
				newTerms = append(newTerms, term)
			}
		}
		if current == math.MaxInt64 || nextID < 1 || nextID > int64(math.MaxUint32)+1 ||
			int64(len(newTerms)) > int64(math.MaxUint32)+1-nextID {
			return nil, 0, errors.New("dictionary revision or uint32 term IDs exhausted")
		}
		newRevision := current + 1
		startID := nextID
		command, insertErr := tx.Exec(ctx, `INSERT INTO lexical_dictionary_terms
			(corpus_id,analyzer_id,term,term_id,from_revision)
			SELECT $1,$2,entry.term,$4 + entry.position - 1,$5
			FROM unnest($3::text[]) WITH ORDINALITY AS entry(term,position)`,
			corpusID, analyzerID, newTerms, startID, newRevision)
		if insertErr != nil {
			return nil, 0, fmt.Errorf("insert dictionary terms: %w", insertErr)
		}
		if command.RowsAffected() != int64(len(newTerms)) {
			return nil, 0, fmt.Errorf("dictionary insert count mismatch: %w", domain.ErrPersistentIntegrity)
		}
		for index, term := range newTerms {
			mapping[term] = LexicalTerm{Term: term, ID: uint32(startID + int64(index))}
		}
		nextID += int64(len(newTerms))
		current = newRevision
		if _, err = tx.Exec(ctx, `UPDATE lexical_dictionary_state SET revision=$3,next_term_id=$4
			WHERE corpus_id=$1 AND analyzer_id=$2`, corpusID, analyzerID, current, nextID); err != nil {
			return nil, 0, fmt.Errorf("advance dictionary state: %w", err)
		}
	}
	if !replay {
		if _, err = tx.Exec(ctx, `INSERT INTO lexical_dictionary_operations
			(corpus_id,analyzer_id,operation_key,terms_hash,term_count,revision)
			VALUES ($1,$2,$3,$4,$5,$6)`, corpusID, analyzerID, operationKey, digest, len(ordered), current); err != nil {
			return nil, 0, fmt.Errorf("record dictionary operation: %w", err)
		}
	} else {
		current = oldRevision
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, 0, fmt.Errorf("commit dictionary allocation: %w", err)
	}
	result := make([]LexicalTerm, len(terms))
	for i, term := range terms {
		result[i] = mapping[term]
	}
	return result, uint64(current), nil
}

// LoadLexicalDictionary reads a caller-pinned revision with an explicit memory bound.
// The append-only SQL rows make the returned mapping stable across later allocations.
func (r *Repository) LoadLexicalDictionary(ctx context.Context, corpusID, analyzerID string,
	revision uint64, maxTerms int) ([]LexicalTerm, error) {
	if r == nil || r.pool == nil {
		return nil, errors.New("postgres repository is closed")
	}
	if !storageIDPattern.MatchString(corpusID) || !storageIDPattern.MatchString(analyzerID) ||
		revision == 0 || revision > math.MaxInt64 || maxTerms < 1 || maxTerms > 10_000_000 {
		return nil, errors.New("invalid bounded dictionary read")
	}
	var current int64
	if err := r.pool.QueryRow(ctx, `SELECT revision FROM lexical_dictionary_state
		WHERE corpus_id=$1 AND analyzer_id=$2`, corpusID, analyzerID).Scan(&current); err != nil {
		return nil, fmt.Errorf("read dictionary revision: %w", err)
	}
	if revision > uint64(current) {
		return nil, fmt.Errorf("dictionary revision not allocated: %w", ErrConflict)
	}
	rows, err := r.pool.Query(ctx, `SELECT term,term_id FROM lexical_dictionary_terms
		WHERE corpus_id=$1 AND analyzer_id=$2 AND from_revision<=$3 ORDER BY term_id LIMIT $4`,
		corpusID, analyzerID, int64(revision), maxTerms+1)
	if err != nil {
		return nil, fmt.Errorf("query dictionary terms: %w", err)
	}
	defer rows.Close()
	result := make([]LexicalTerm, 0, min(maxTerms, 1024))
	for rows.Next() {
		var term string
		var id int64
		if err = rows.Scan(&term, &id); err != nil {
			return nil, fmt.Errorf("scan dictionary term: %w", err)
		}
		if len(result) == maxTerms {
			return nil, ErrResultLimit
		}
		if id < 1 || id > math.MaxUint32 {
			return nil, fmt.Errorf("dictionary term ID invalid: %w", domain.ErrPersistentIntegrity)
		}
		result = append(result, LexicalTerm{Term: term, ID: uint32(id)})
	}
	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate dictionary terms: %w", err)
	}
	return result, nil
}

func validateLexicalTerms(corpusID, analyzerID, operationKey string, expectedRevision uint64,
	terms []string) ([]string, string, error) {
	if !storageIDPattern.MatchString(corpusID) || !storageIDPattern.MatchString(analyzerID) ||
		!storageIDPattern.MatchString(operationKey) || expectedRevision > math.MaxInt64 ||
		len(terms) == 0 || len(terms) > maxLexicalAllocationTerms {
		return nil, "", errors.New("invalid bounded dictionary allocation")
	}
	ordered := append([]string(nil), terms...)
	sort.Strings(ordered)
	for i, term := range ordered {
		if term == "" || len(term) > 256 || !utf8.ValidString(term) ||
			(i > 0 && term == ordered[i-1]) {
			return nil, "", errors.New("invalid or duplicate lexical term")
		}
		for _, ch := range term {
			if unicode.IsControl(ch) {
				return nil, "", errors.New("lexical term contains control character")
			}
		}
	}
	h := sha256.New()
	h.Write([]byte("regulagraph-lexical-allocation-v1\x00"))
	var size [8]byte
	for _, term := range ordered {
		binary.BigEndian.PutUint64(size[:], uint64(len(term)))
		h.Write(size[:])
		h.Write([]byte(term))
	}
	return ordered, hex.EncodeToString(h.Sum(nil)), nil
}
