package postgres

import (
	"database/sql"
	"fmt"
	"sync"
)

// attributeKeyCache resolves Sachdaten attribute key names to their
// internal attribute_key.id — see pkg/sqlite/attribute_key.go's
// identical type for the full design rationale (lazy get-or-create, not a
// pre-seeded fixed Go enum; concurrent readers, no concurrent writers to
// the same central database).
type attributeKeyCache struct {
	mu     sync.RWMutex
	byName map[string]int64
}

// loadAttributeKeyCache reads every row of attribute_key into a fresh
// cache — called once per ModelStore (see ModelStore.attributeKeys).
func loadAttributeKeyCache(db *sql.DB) (*attributeKeyCache, error) {
	c := &attributeKeyCache{byName: make(map[string]int64)}
	rows, err := db.Query(`SELECT id, name FROM attribute_key`)
	if err != nil {
		return nil, fmt.Errorf("postgres: loading attribute_key cache: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var name string
		if err := rows.Scan(&id, &name); err != nil {
			return nil, fmt.Errorf("postgres: scanning attribute_key row: %w", err)
		}
		c.byName[name] = id
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres: iterating attribute_key rows: %w", err)
	}
	return c, nil
}

// resolve returns name's attribute_key.id, creating the row (via
// INSERT ... ON CONFLICT DO NOTHING, then a SELECT to obtain the id
// whether this call or a concurrent process/transaction won the insert)
// the first time name is seen by this process. tx is the caller's
// already-open transaction, so the new row is visible to the rest of the
// same batch's writes without waiting for a separate commit.
func (c *attributeKeyCache) resolve(tx *sql.Tx, name string) (int64, error) {
	c.mu.RLock()
	id, ok := c.byName[name]
	c.mu.RUnlock()
	if ok {
		return id, nil
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	// Re-check under the write lock: another goroutine may have already
	// resolved (and cached) this exact name while we were waiting.
	if id, ok := c.byName[name]; ok {
		return id, nil
	}

	if _, err := tx.Exec(rebind(`INSERT INTO attribute_key (name) VALUES (?) ON CONFLICT (name) DO NOTHING`), name); err != nil {
		return 0, fmt.Errorf("postgres: inserting new attribute_key %q: %w", name, err)
	}
	var id2 int64
	if err := tx.QueryRow(rebind(`SELECT id FROM attribute_key WHERE name = ?`), name).Scan(&id2); err != nil {
		return 0, fmt.Errorf("postgres: resolving attribute_key id for %q: %w", name, err)
	}
	c.byName[name] = id2
	return id2, nil
}
