// Menyediakan adapter penulisan graph dan eksekusi query yang diminta komponen.
//
// Peran dalam komponen:
// Menghubungkan assembly serta traversal ke Neo4j.
//
// Kontrak integrasi dan perhatian implementasi:
// Gunakan parameter dan identitas stabil; pembatasan resource query harus terlihat ke caller, bukan diam-diam menghilangkan bukti.
//
// Benchmark dan gate penerimaan:
// [GRAPH] Ukur validitas endpoint dan provenance, ketepatan predicate/arah, kelengkapan jalur bukti, waktu assembly/traversal, dan penggunaan memori. Gate: graph yang dipublikasikan tidak memiliki endpoint/bukti wajib yang hilang. Connectivity adalah diagnosis, bukan target memaksa satu komponen.
//
// [STORAGE] Ukur latency p50/p95/p99, throughput batch, pool saturation, retry, dan error rate pada concurrency serta volume data yang disebutkan. Gate: timeout terlapor, resource dilepas, dan operasi tulis idempotent sesuai kontrak; tidak ada asumsi transaksi atomik lintas layanan.
// Target numerik required: configs/benchmark-targets.yaml; status REQUIRED_UNMEASURED.
// Target hanya boleh diubah dengan persetujuan pengguna; ikuti doc/benchmark-policy.md.
//
// Status: driver Bolt reusable dengan transaksi eksplisit dan generation graph awal
// immutable. Apply/verify/seal tidak mengubah pointer publication PostgreSQL. Binding
// hanya boleh datang dari coordinator terpercaya setelah admission GraphDelta.
// Closure incremental, takeover writer dan readiness multi-replica belum tersedia.
// Bukti verifikasi: Test replay, shared-support retention and search-ready receipts on real Neo4j; profile query plans and fan-out.
// Target numerik tetap configs/benchmark-targets.yaml; ikuti doc/verification.md.

package neo4j

import (
	"context"
	"errors"
	"sync/atomic"
	"time"

	bolt "github.com/neo4j/neo4j-go-driver/v5/neo4j"
	"google.golang.org/protobuf/proto"
	pb "regulagraph.local/server/gen/regulagraph/v1"
	"regulagraph.local/server/internal/domain"
)

var ErrGraphConflict = errors.Join(errors.New("graph generation or immutable record conflict"), domain.ErrPersistentIntegrity)

// Binding is a local backend routing/ownership value, not a second wire schema.
// A generation has exactly one publication/fence/base/target/revision. Publication
// authority remains PostgreSQL; an unsealed or aborted generation is never served.
type Binding = domain.GraphBinding

type Config struct {
	URI, Username, Password, Database string
	PoolSize                          int
	Timeout                           time.Duration
}

type Store struct {
	driver      bolt.DriverWithContext
	database    string
	endpoint    string
	timeout     time.Duration
	binding     Binding
	bindingHash string
	schemaReady atomic.Bool
}

// New validates configuration without opening a connection. Direct Bolt is used
// deliberately: this adapter has no proof of readiness across routed replicas.
func New(config Config, binding Binding) (*Store, error) {
	if err := domain.ValidateGraphEndpoint(config.URI); err != nil {
		return nil, err
	}
	if config.Database == "" || config.Username == "" || config.Password == "" || config.PoolSize < 1 || config.PoolSize > 128 || config.Timeout <= 0 || config.Timeout > 5*time.Minute {
		return nil, errors.New("explicit database, credentials and bounded pool/timeout required")
	}
	hash, err := domain.GraphBindingHash(binding)
	if err != nil {
		return nil, err
	}
	binding.BaseSnapshot = proto.Clone(binding.BaseSnapshot).(*pb.SnapshotRef)
	driver, err := bolt.NewDriverWithContext(config.URI, bolt.BasicAuth(config.Username, config.Password, ""), func(c *bolt.Config) {
		c.MaxConnectionPoolSize = config.PoolSize
		c.ConnectionAcquisitionTimeout = config.Timeout
		c.SocketConnectTimeout = config.Timeout
		c.MaxTransactionRetryTime = 0
		c.FetchSize = 256
		c.TelemetryDisabled = true
	})
	if err != nil {
		return nil, errors.New("invalid Neo4j driver configuration")
	}
	return &Store{driver: driver, database: config.Database, timeout: config.Timeout, binding: binding, bindingHash: hash, endpoint: config.URI}, nil
}

func (s *Store) Endpoint() string { return s.endpoint }
func (s *Store) Database() string { return s.database }
func (s *Store) Binding() Binding {
	result := s.binding
	result.BaseSnapshot = proto.Clone(result.BaseSnapshot).(*pb.SnapshotRef)
	return result
}

func (s *Store) Close(ctx context.Context) error { return s.driver.Close(ctx) }

func (s *Store) transaction(ctx context.Context, write bool, run func(context.Context, bolt.ExplicitTransaction) error) error {
	if ctx == nil {
		return errors.New("context required")
	}
	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	mode := bolt.AccessModeRead
	if write {
		mode = bolt.AccessModeWrite
	}
	session := s.driver.NewSession(ctx, bolt.SessionConfig{DatabaseName: s.database, AccessMode: mode, FetchSize: 256})
	defer func() {
		cleanup, done := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
		defer done()
		_ = session.Close(cleanup)
	}()
	tx, err := session.BeginTransaction(ctx, bolt.WithTxTimeout(s.timeout))
	if err != nil {
		return err
	}
	defer func() {
		cleanup, done := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
		defer done()
		_ = tx.Close(cleanup)
	}()
	if err = run(ctx, tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) params() map[string]any {
	return map[string]any{"corpus": s.binding.CorpusID, "generation": s.binding.Generation, "binding": s.bindingHash, "sequence": int64(s.binding.Sequence)}
}

func one(ctx context.Context, tx bolt.ExplicitTransaction, query string, params map[string]any) (*bolt.Record, error) {
	r, err := tx.Run(ctx, query, params)
	if err != nil {
		return nil, err
	}
	return r.Single(ctx)
}

// lockGeneration serializes writes and sealing inside Neo4j, not just in Go.
// Incrementing a property takes the database write lock before checking binding.
func (s *Store) lockGeneration(ctx context.Context, tx bolt.ExplicitTransaction) (bool, error) {
	r, err := one(ctx, tx, `MERGE (g:RGGeneration {corpus:$corpus,generation:$generation})
ON CREATE SET g.binding=$binding,g.state='OPEN',g.sequence=$sequence,g.lock=0,g.operations=0,g.bytes=0
SET g.lock=g.lock+1 RETURN g.binding AS binding,g.state AS state`, s.params())
	if err != nil {
		return false, err
	}
	if r.Values[0] != s.bindingHash {
		return false, ErrGraphConflict
	}
	if r.Values[1] != "OPEN" && r.Values[1] != "SEALED" {
		return false, ErrGraphConflict
	}
	return r.Values[1] == "SEALED", nil
}
