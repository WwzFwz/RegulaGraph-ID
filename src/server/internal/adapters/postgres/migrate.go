// Menerapkan migration PostgreSQL bernomor secara eksplisit dan dapat diaudit.
// Peran: membentuk schema durable S01 sebelum adapter job/publication menerima traffic.
// Kontrak: advisory lock menserialkan runner, checksum SHA-256 mencegah version drift,
// setiap *.up.sql transaksional, dan import/startup tidak memutasi schema otomatis.
// Benchmark: ukur startup migration dan lock wait secara terpisah dari latency request;
// migration besar wajib memiliki strategi rollout sendiri sebelum produksi.
// Target numerik required: configs/benchmark-targets.yaml; status REQUIRED_UNMEASURED.
// Status: discovery, ordering, checksum replay, dan transactional apply S01 aktif.
package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"
)

const migrationAdvisoryLock int64 = 0x524547554c415331

func (r *Repository) ApplyMigrations(ctx context.Context, migrations fs.FS) error {
	conn, err := r.pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("acquire migration connection: %w", err)
	}
	defer conn.Release()
	if _, err = conn.Exec(ctx, "SELECT pg_advisory_lock($1)", migrationAdvisoryLock); err != nil {
		return fmt.Errorf("lock migrations: %w", err)
	}
	defer conn.Exec(context.Background(), "SELECT pg_advisory_unlock($1)", migrationAdvisoryLock)
	if _, err = conn.Exec(ctx, `CREATE TABLE IF NOT EXISTS app_schema_migrations (
        version text PRIMARY KEY,
        checksum_sha256 text NOT NULL,
        applied_at timestamptz NOT NULL DEFAULT clock_timestamp())`); err != nil {
		return fmt.Errorf("bootstrap migration table: %w", err)
	}
	entries, err := fs.ReadDir(migrations, ".")
	if err != nil {
		return fmt.Errorf("read migrations: %w", err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".up.sql") {
			names = append(names, entry.Name())
		}
	}
	sort.Strings(names)
	for _, name := range names {
		body, readErr := fs.ReadFile(migrations, name)
		if readErr != nil {
			return fmt.Errorf("read migration %s: %w", name, readErr)
		}
		digestBytes := sha256.Sum256(body)
		digest := hex.EncodeToString(digestBytes[:])
		var stored string
		err = conn.QueryRow(ctx, "SELECT checksum_sha256 FROM app_schema_migrations WHERE version=$1", name).Scan(&stored)
		if err == nil {
			if stored != digest {
				return fmt.Errorf("migration %s checksum changed: %w", name, ErrConflict)
			}
			continue
		}
		if err != pgx.ErrNoRows {
			return fmt.Errorf("inspect migration %s: %w", name, err)
		}
		tx, beginErr := conn.Begin(ctx)
		if beginErr != nil {
			return fmt.Errorf("begin migration %s: %w", name, beginErr)
		}
		if _, err = tx.Exec(ctx, string(body)); err == nil {
			_, err = tx.Exec(ctx, "INSERT INTO app_schema_migrations(version, checksum_sha256) VALUES ($1,$2)", name, digest)
		}
		if err != nil {
			tx.Rollback(ctx)
			return fmt.Errorf("apply migration %s: %w", name, err)
		}
		if err = tx.Commit(ctx); err != nil {
			return fmt.Errorf("commit migration %s: %w", name, err)
		}
	}
	return nil
}
