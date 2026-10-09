// Installs and verifies fixed Neo4j identity constraints for isolated graph
// generations, records and operation receipts. Schema setup is explicit and
// idempotent; an existing constraint with the wrong definition is rejected.
// This is not publication readiness. Measure setup separately from warm writes.
package neo4j

import (
	"context"
	"fmt"
	"reflect"

	bolt "github.com/neo4j/neo4j-go-driver/v5/neo4j"
)

func (s *Store) EnsureSchema(ctx context.Context) error {
	for _, definition := range []struct {
		name, label string
		properties  []string
		query       string
	}{
		{"rg_generation_v1", "RGGeneration", []string{"corpus", "generation"}, `CREATE CONSTRAINT rg_generation_v1 IF NOT EXISTS FOR (n:RGGeneration) REQUIRE (n.corpus,n.generation) IS UNIQUE`},
		{"rg_record_v1", "RGRecord", []string{"corpus", "generation", "id"}, `CREATE CONSTRAINT rg_record_v1 IF NOT EXISTS FOR (n:RGRecord) REQUIRE (n.corpus,n.generation,n.id) IS UNIQUE`},
		{"rg_operation_v1", "RGOperation", []string{"corpus", "generation", "id"}, `CREATE CONSTRAINT rg_operation_v1 IF NOT EXISTS FOR (n:RGOperation) REQUIRE (n.corpus,n.generation,n.id) IS UNIQUE`},
	} {
		if err := s.transaction(ctx, true, func(ctx context.Context, tx bolt.ExplicitTransaction) error {
			r, err := tx.Run(ctx, definition.query, nil)
			if err != nil {
				return err
			}
			_, err = r.Consume(ctx)
			return err
		}); err != nil {
			return err
		}
		if err := s.transaction(ctx, false, func(ctx context.Context, tx bolt.ExplicitTransaction) error {
			r, err := one(ctx, tx, `SHOW CONSTRAINTS YIELD name,type,entityType,labelsOrTypes,properties WHERE name=$name RETURN type,entityType,labelsOrTypes,properties`, map[string]any{"name": definition.name})
			if err != nil {
				return err
			}
			props := make([]any, len(definition.properties))
			for i, p := range definition.properties {
				props[i] = p
			}
			if r.Values[0] != "UNIQUENESS" || r.Values[1] != "NODE" || !reflect.DeepEqual(r.Values[2], []any{definition.label}) || !reflect.DeepEqual(r.Values[3], props) {
				return fmt.Errorf("graph constraint %s differs: %w", definition.name, ErrGraphConflict)
			}
			return nil
		}); err != nil {
			return err
		}
	}
	if err := s.ensureTraversalIndexes(ctx); err != nil {
		return err
	}
	s.schemaReady.Store(true)
	return nil
}
