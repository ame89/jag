package sqlite

import (
	"database/sql"
	"fmt"
	"sync"
)

// entityIDCache resolves Node/Edge/Container/Equipment TEXT IDs (e.g.
// "H-9860-CN2") to their internal entity_id.id, normalizing the raw TEXT
// ID that used to be repeated on every model_edge_endpoint,
// model_attribute_value, and model_geometry row — mirroring
// attributeKeyCache's exact design for the same reason (see
// attribute_key.go's doc comment): the ID set is not a fixed, pre-seeded
// enum (new equipment/node/container IDs are created by every import),
// so it is resolved lazily (get-or-create on first use), not migrated in
// bulk. Known IDs never need a database round trip again.
//
// Deliberately scoped to only these three high-volume tables (the ones
// explicitly identified as the DB's biggest size drivers) — model_node,
// model_edge, model_container, and model_equipment keep their own TEXT
// primary keys unchanged, so this cache is purely an internal storage
// detail for the tables that reference those IDs, not a wholesale ID
// scheme change. Reads translate back to the original TEXT ID via either
// a compatibility VIEW (model_geometry, model_attribute) or an explicit
// JOIN back to entity_id (model_edge_endpoint, which has no external
// readers and so needs no VIEW) — see model.go's GetEdgesByNodeIDs and
// GetReachableNodes.
type entityIDCache struct {
	mu   sync.RWMutex
	byID map[string]int64
}

// loadEntityIDCache reads every row of entity_id into a fresh cache —
// called once per ModelStore (see ModelStore.entityIDs).
func loadEntityIDCache(db *sql.DB) (*entityIDCache, error) {
	c := &entityIDCache{byID: make(map[string]int64)}
	rows, err := db.Query(`SELECT id, external_id FROM entity_id`)
	if err != nil {
		return nil, fmt.Errorf("sqlite: loading entity_id cache: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var externalID string
		if err := rows.Scan(&id, &externalID); err != nil {
			return nil, fmt.Errorf("sqlite: scanning entity_id row: %w", err)
		}
		c.byID[externalID] = id
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite: iterating entity_id rows: %w", err)
	}
	return c, nil
}

// resolve returns externalID's entity_id.id, creating the row (via
// INSERT ... ON CONFLICT DO NOTHING, then a SELECT to obtain the id
// whether this call or a race won the insert) the first time externalID
// is seen by this process. tx is the caller's already-open transaction,
// so the new row is visible to the rest of the same batch's writes
// without waiting for a separate commit.
func (c *entityIDCache) resolve(tx *sql.Tx, externalID string) (int64, error) {
	c.mu.RLock()
	id, ok := c.byID[externalID]
	c.mu.RUnlock()
	if ok {
		return id, nil
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	// Re-check under the write lock: another goroutine may have already
	// resolved (and cached) this exact ID while we were waiting.
	if id, ok := c.byID[externalID]; ok {
		return id, nil
	}

	if _, err := tx.Exec(`INSERT INTO entity_id (external_id) VALUES (?) ON CONFLICT (external_id) DO NOTHING`, externalID); err != nil {
		return 0, fmt.Errorf("sqlite: inserting new entity_id %q: %w", externalID, err)
	}
	var id2 int64
	if err := tx.QueryRow(`SELECT id FROM entity_id WHERE external_id = ?`, externalID).Scan(&id2); err != nil {
		return 0, fmt.Errorf("sqlite: resolving entity_id id for %q: %w", externalID, err)
	}
	c.byID[externalID] = id2
	return id2, nil
}
