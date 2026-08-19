package sqlite

import (
	"database/sql"
	"fmt"
	"sync"
)

// attributeKeyCache resolves Sachdaten attribute key names (see
// coremodel.AttributeKey) to their internal attribute_key.id, normalizing
// what used to be a raw TEXT key repeated on every model_attribute row.
//
// Design (see Konzept.md's Sachdaten discussion): the key set is small,
// curated, and only ever grows (no deletion/rename) — but it is NOT a
// fixed, pre-seeded Go enum, since new keys (e.g. previously-unseen CIM
// attribute names) must be usable without recompiling/redeploying JAG.
// The cache therefore loads every known key once (loadAttributeKeyCache,
// called lazily on first use of a ModelStore) and resolves unknown names
// on demand (resolve), inserting a new attribute_key row the first time a
// name is seen. Known names never need a database round trip again.
//
// Concurrency: within one process, resolve is safe for concurrent callers
// (e.g. Pass A's parallel station workers) via a plain RWMutex — reads
// (the overwhelmingly common case: a key already known) take a cheap
// RLock; only the rare "first time this name is seen" path takes the
// write lock and touches the database. Cross-process concurrent writers
// to the same central database are deliberately out of scope (see
// Konzept.md — concurrent writers to a shared database are considered an
// anti-pattern; only concurrent *readers*, e.g. different Netzregionen
// imported by separate processes, are expected) — a process that
// encounters a name another process just created simply does its own
// get-or-create round trip (INSERT ... ON CONFLICT DO NOTHING + SELECT),
// which is correct regardless of timing, without any need to learn about
// other processes' inserts proactively.
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
		return nil, fmt.Errorf("sqlite: loading attribute_key cache: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var name string
		if err := rows.Scan(&id, &name); err != nil {
			return nil, fmt.Errorf("sqlite: scanning attribute_key row: %w", err)
		}
		c.byName[name] = id
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite: iterating attribute_key rows: %w", err)
	}
	return c, nil
}

// resolve returns name's attribute_key.id, creating the row (via
// INSERT ... ON CONFLICT DO NOTHING, then a SELECT to obtain the id
// whether this call or a race won the insert) the first time name is seen
// by this process. tx is the caller's already-open transaction, so the
// new row is visible to the rest of the same batch's writes without
// waiting for a separate commit.
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

	if _, err := tx.Exec(`INSERT INTO attribute_key (name) VALUES (?) ON CONFLICT (name) DO NOTHING`, name); err != nil {
		return 0, fmt.Errorf("sqlite: inserting new attribute_key %q: %w", name, err)
	}
	var id2 int64
	if err := tx.QueryRow(`SELECT id FROM attribute_key WHERE name = ?`, name).Scan(&id2); err != nil {
		return 0, fmt.Errorf("sqlite: resolving attribute_key id for %q: %w", name, err)
	}
	c.byName[name] = id2
	return id2, nil
}
