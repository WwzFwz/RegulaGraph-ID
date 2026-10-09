// Streams immutable index catalog records under a live source snapshot pin for
// full readback before generation reuse. Keyset pages are at most 64 records and
// 512 KiB; oversized individual records fail explicitly. No corpus-sized list is
// allocated. The existing record reader validates IDs, payloads and visibility.
// Measure page/readback p95, total bytes and RSS under benchmark-targets.yaml.
package postgres

import (
	"context"
	"errors"

	"regulagraph.local/server/internal/domain"
)

func (r *Repository) ReadPinnedIndexPage(ctx context.Context, pin domain.SnapshotPin, after string) ([]domain.IndexCatalogRecord, error) {
	if err := validateIndexPin(pin); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithDeadline(ctx, pin.ExpiresAt)
	defer cancel()
	if after != "" && !storageIDPattern.MatchString(after) {
		return nil, errors.New("invalid index page cursor")
	}
	index, err := r.LoadPinnedIndex(ctx, pin)
	if err != nil {
		return nil, err
	}
	rows, err := r.pool.Query(ctx, `SELECT record_id,octet_length(record_payload) FROM index_points WHERE corpus_id=$1 AND generation_id=$2 AND record_id>$3 ORDER BY record_id LIMIT 64`, pin.CorpusID, index.Binding.Generation.Meta.RecordId, after)
	if err != nil {
		return nil, err
	}
	ids := []string{}
	remaining := 512 << 10
	for rows.Next() {
		var id string
		var size int
		if err = rows.Scan(&id, &size); err != nil {
			break
		}
		if size > remaining {
			if len(ids) == 0 {
				err = ErrResultLimit
			}
			break
		}
		remaining -= size
		ids = append(ids, id)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return nil, err
	}
	return r.LoadPinnedIndexRecords(ctx, pin, ids, 512<<10)
}
