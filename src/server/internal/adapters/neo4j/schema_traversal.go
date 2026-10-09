// Bootstraps composite projection indexes for bounded graph discovery. Unioning
// indexed assertion/support membership with physical adjacency detects missing
// links without scanning every graph record into Go. Setup is explicit; readers
// never create schema. Verify index definitions and await ONLINE under the store
// deadline; measure bootstrap separately from warm traversal fan-out and p95.
package neo4j

import (
	"context"
	"fmt"
	"reflect"

	bolt "github.com/neo4j/neo4j-go-driver/v5/neo4j"
)

func (s *Store) ensureTraversalIndexes(ctx context.Context) error {
	for _, property := range []string{"subject", "object", "assertion"} {
		name := "rg_record_" + property + "_v1"
		// Identifiers come only from this closed vocabulary, never caller input.
		if err := s.transaction(ctx, true, func(c context.Context, tx bolt.ExplicitTransaction) error {
			rows, e := tx.Run(c, `CREATE RANGE INDEX `+name+` IF NOT EXISTS FOR (n:RGRecord) ON (n.corpus,n.generation,n.`+property+`)`, nil)
			if e != nil {
				return e
			}
			_, e = rows.Consume(c)
			return e
		}); err != nil {
			return err
		}
		if err := s.transaction(ctx, false, func(c context.Context, tx bolt.ExplicitTransaction) error {
			p := map[string]any{"name": name, "timeout": int64(s.timeout.Seconds()) + 1}
			rows, e := tx.Run(c, `CALL db.awaitIndex($name,$timeout)`, p)
			if e != nil {
				return e
			}
			if _, e = rows.Consume(c); e != nil {
				return e
			}
			row, e := one(c, tx, `SHOW INDEXES YIELD name,type,entityType,labelsOrTypes,properties,state WHERE name=$name RETURN type,entityType,labelsOrTypes,properties,state`, p)
			if e != nil {
				return e
			}
			if row.Values[0] != "RANGE" || row.Values[1] != "NODE" || !reflect.DeepEqual(row.Values[2], []any{"RGRecord"}) || !reflect.DeepEqual(row.Values[3], []any{"corpus", "generation", property}) || row.Values[4] != "ONLINE" {
				return fmt.Errorf("graph index %s differs: %w", name, ErrGraphConflict)
			}
			return nil
		}); err != nil {
			return err
		}
	}
	return nil
}
