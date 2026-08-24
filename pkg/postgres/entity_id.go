package postgres

import (
	"database/sql"
	"fmt"
	"sync"
)

// entityIDCache resolves Node/Edge/Container/Equipment TEXT IDs to their
// internal entity_id.id — see pkg/sqlite/entity_id.go's identical type
// for the full design rationale (lazy get-or-create, scoped only to
// model_edge_endpoint/model_attribute_value/model_geometry, the three
// tables explicitly identified as the DB's biggest size drivers).
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
		return nil, fmt.Errorf("postgres: loading entity_id cache: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var externalID string
		if err := rows.Scan(&id, &externalID); err != nil {
			return nil, fmt.Errorf("postgres: scanning entity_id row: %w", err)
		}
		c.byID[externalID] = id
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres: iterating entity_id rows: %w", err)
	}
	return c, nil
}

// resolve returns externalID's entity_id.id, creating the row (via
// INSERT ... ON CONFLICT DO NOTHING, then a SELECT to obtain the id
// whether this call or a concurrent process/transaction won the insert)
// the first time externalID is seen by this process. tx is the caller's
// already-open transaction, so the new row is visible to the rest of the
// same batch's writes without waiting for a separate commit.
func (c *entityIDCache) resolve(tx *sql.Tx, externalID string) (int64, error) {
	c.mu.RLock()
	id, ok := c.byID[externalID]
	c.mu.RUnlock()
	if ok {
		return id, nil
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if id, ok := c.byID[externalID]; ok {
		return id, nil
	}

	if _, err := tx.Exec(rebind(`INSERT INTO entity_id (external_id) VALUES (?) ON CONFLICT (external_id) DO NOTHING`), externalID); err != nil {
		return 0, fmt.Errorf("postgres: inserting new entity_id %q: %w", externalID, err)
	}
	var id2 int64
	if err := tx.QueryRow(rebind(`SELECT id FROM entity_id WHERE external_id = ?`), externalID).Scan(&id2); err != nil {
		return 0, fmt.Errorf("postgres: resolving entity_id id for %q: %w", externalID, err)
	}
	c.byID[externalID] = id2
	return id2, nil
}

// lookup is resolve's read-only counterpart: it reports whether
// externalID already has an entity_id row, without creating one when it
// doesn't — see pkg/sqlite/entity_id.go's identical lookup for the full
// rationale (used by deletion paths such as DeleteContainers, which only
// ever want to act on IDs that already own attribute_value/geometry_value
// rows).
func (c *entityIDCache) lookup(tx *sql.Tx, externalID string) (int64, bool, error) {
	c.mu.RLock()
	id, ok := c.byID[externalID]
	c.mu.RUnlock()
	if ok {
		return id, true, nil
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if id, ok := c.byID[externalID]; ok {
		return id, true, nil
	}
	var id2 int64
	err := tx.QueryRow(rebind(`SELECT id FROM entity_id WHERE external_id = ?`), externalID).Scan(&id2)
	if err == sql.ErrNoRows {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("postgres: looking up entity_id for %q: %w", externalID, err)
	}
	c.byID[externalID] = id2
	return id2, true, nil
}

// resolveMany resolves a batch of external IDs at once, deduplicating and
// only round-tripping to the database for IDs not already cached. This is
// the write path's bulk equivalent of resolve — used by upsertGeometryTx/
// upsertEdgesTx/upsertAttributesTx so the chunked multi-row INSERT
// strategy used throughout this package (see insertChunkSize) still
// applies even though ids now need translating first.
func (c *entityIDCache) resolveMany(tx *sql.Tx, externalIDs []string) (map[string]int64, error) {
	result := make(map[string]int64, len(externalIDs))
	var missing []string
	c.mu.RLock()
	for _, id := range externalIDs {
		if key, ok := c.byID[id]; ok {
			result[id] = key
		} else if _, dup := result[id]; !dup {
			missing = append(missing, id)
		}
	}
	c.mu.RUnlock()

	for _, id := range missing {
		if _, ok := result[id]; ok {
			continue
		}
		key, err := c.resolve(tx, id)
		if err != nil {
			return nil, err
		}
		result[id] = key
	}
	return result, nil
}
