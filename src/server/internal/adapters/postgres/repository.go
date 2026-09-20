// Menyediakan adapter penyimpanan metadata, versi, dan manifest eksekusi PostgreSQL.
//
// Peran dalam komponen:
// Melaksanakan kebutuhan persistensi komponen tanpa membawa logika domain ke SQL client.
// Repository adalah control-plane durable untuk job, fence, publication ledger, registry
// revision, dan pointer snapshot; Neo4j, Qdrant, serta artifact store tetap backend terpisah.
//
// Kontrak integrasi dan perhatian implementasi:
// Gunakan transaksi lokal, constraint ID, pool koneksi yang dipakai ulang, query berparameter,
// dan migration version. Import/inisialisasi paket tidak membuka koneksi. Repository dibuat
// eksplisit melalui Open, dan transaksi PostgreSQL tidak dianggap mencakup Neo4j/Qdrant.
//
// Benchmark dan gate penerimaan:
// [STORAGE] Ukur latency p50/p95/p99, throughput batch, pool saturation, retry, dan error
// rate pada concurrency serta volume data yang disebutkan. Gate: timeout terlapor, resource
// dilepas, dan operasi tulis idempotent sesuai kontrak; tidak ada asumsi transaksi atomik
// lintas layanan.
// [UPDATE] Gate: sumber tak berubah tidak diekstraksi ulang; update identik idempotent;
// dependensi terdampak diinvalidasi; bukti yang masih didukung sumber lain tetap tersedia.
// Uji pemulihan kegagalan parsial dan snapshot konsisten. Ukur waktu/biaya update terhadap
// full rebuild serta freshness lag.
// Target numerik required: configs/benchmark-targets.yaml; status REQUIRED_UNMEASURED.
// Target hanya boleh diubah dengan persetujuan pengguna; ikuti doc/benchmark-policy.md.
//
// Status: implementasi S01 aktif untuk lifecycle pool dan error boundary. Operasi job,
// artifact metadata, migration, serta publication berada pada file anak paket ini.
// Bukti verifikasi wajib: uniqueness, concurrent claim/CAS, crash recovery, historical
// visibility, dan pool saturation pada PostgreSQL aktual; ikuti doc/verification.md.
package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrConflict            = errors.New("storage conflict")
	ErrNotFound            = errors.New("record not found")
	ErrStaleFence          = errors.New("stale lease or publication fence")
	ErrLeaseUnavailable    = errors.New("no claimable job")
	ErrPublicationNotReady = errors.New("publication is not ready")
	ErrSnapshotCASConflict = errors.New("active snapshot compare-and-swap conflict")
)

type Config struct {
	DSN            string
	MaxConnections int32
	MinConnections int32
	ConnectTimeout time.Duration
	HealthTimeout  time.Duration
}

type Repository struct {
	pool *pgxpool.Pool
}

func Open(ctx context.Context, cfg Config) (*Repository, error) {
	if cfg.DSN == "" {
		return nil, errors.New("postgres DSN is required")
	}
	parsed, err := pgxpool.ParseConfig(cfg.DSN)
	if err != nil {
		return nil, fmt.Errorf("parse postgres config: %w", err)
	}
	if cfg.MaxConnections > 0 {
		parsed.MaxConns = cfg.MaxConnections
	}
	if cfg.MinConnections > 0 {
		parsed.MinConns = cfg.MinConnections
	}
	if cfg.ConnectTimeout > 0 {
		parsed.ConnConfig.ConnectTimeout = cfg.ConnectTimeout
	}
	pool, err := pgxpool.NewWithConfig(ctx, parsed)
	if err != nil {
		return nil, fmt.Errorf("create postgres pool: %w", err)
	}
	repo := &Repository{pool: pool}
	healthCtx := ctx
	var cancel context.CancelFunc
	if cfg.HealthTimeout > 0 {
		healthCtx, cancel = context.WithTimeout(ctx, cfg.HealthTimeout)
		defer cancel()
	}
	if err := pool.Ping(healthCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping postgres: %w", err)
	}
	return repo, nil
}

func (r *Repository) Close() {
	if r != nil && r.pool != nil {
		r.pool.Close()
	}
}

func (r *Repository) Ping(ctx context.Context) error {
	if r == nil || r.pool == nil {
		return errors.New("postgres repository is closed")
	}
	return r.pool.Ping(ctx)
}
