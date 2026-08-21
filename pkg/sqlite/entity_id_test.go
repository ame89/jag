package sqlite

import (
	"database/sql"
	"testing"
)

// TestEntityIDCache_LookupUnknownID pins lookup's documented contract
// (see entity_id.go's doc comment): for an externalID with no existing
// entity_id row, lookup must report "not found" (ok == false, err ==
// nil) and must NOT create a row as a side effect — unlike resolve, which
// always creates one. This is verified by calling lookup twice (a second
// call would reuse a row resolve would have created on the first call,
// but must still report ok == false here) and by checking a freshly
// loaded cache afterwards sees no persisted row for it.
func TestEntityIDCache_LookupUnknownID(t *testing.T) {
	s, err := Open(":memory:")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()
	m := s.Model()

	cache, err := m.entityIDs()
	if err != nil {
		t.Fatalf("entityIDs: %v", err)
	}

	for i := 0; i < 2; i++ {
		err = withTx(m.db, func(tx *sql.Tx) error {
			id, ok, err := cache.lookup(tx, "never-seen-id")
			if err != nil {
				return err
			}
			if ok {
				t.Fatalf("call %d: lookup(unknown) = (%d, true), want ok=false", i, id)
			}
			return nil
		})
		if err != nil {
			t.Fatalf("withTx: %v", err)
		}
	}

	fresh, err := loadEntityIDCache(m.db)
	if err != nil {
		t.Fatalf("loadEntityIDCache: %v", err)
	}
	if _, ok := fresh.byID["never-seen-id"]; ok {
		t.Fatalf("lookup unexpectedly created a persisted entity_id row for an unknown id")
	}
}

// TestEntityIDCache_LookupFindsResolvedID covers lookup's happy path:
// once resolve has created a row for an ID (e.g. via an Upsert* call
// elsewhere), lookup must find it and return the exact same id, both from
// the in-memory cache and (via a second, freshly-loaded cache instance)
// from the persisted entity_id table.
func TestEntityIDCache_LookupFindsResolvedID(t *testing.T) {
	s, err := Open(":memory:")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()
	m := s.Model()

	cache, err := m.entityIDs()
	if err != nil {
		t.Fatalf("entityIDs: %v", err)
	}

	var resolvedID int64
	err = withTx(m.db, func(tx *sql.Tx) error {
		id, err := cache.resolve(tx, "eq1")
		resolvedID = id
		return err
	})
	if err != nil {
		t.Fatalf("withTx(resolve): %v", err)
	}

	err = withTx(m.db, func(tx *sql.Tx) error {
		id, ok, err := cache.lookup(tx, "eq1")
		if err != nil {
			return err
		}
		if !ok {
			t.Fatalf("lookup(eq1) = (_, false), want ok=true after resolve")
		}
		if id != resolvedID {
			t.Fatalf("lookup(eq1) = %d, want %d (resolve's id)", id, resolvedID)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("withTx(lookup): %v", err)
	}

	// A fresh cache (simulating a new process/ModelStore instance) must
	// also find the persisted row via a cold (non-cache-hit) lookup.
	fresh, err := loadEntityIDCache(m.db)
	if err != nil {
		t.Fatalf("loadEntityIDCache: %v", err)
	}
	err = withTx(m.db, func(tx *sql.Tx) error {
		id, ok, err := fresh.lookup(tx, "eq1")
		if err != nil {
			return err
		}
		if !ok || id != resolvedID {
			t.Fatalf("fresh cache lookup(eq1) = (%d, %v), want (%d, true)", id, ok, resolvedID)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("withTx(fresh lookup): %v", err)
	}
}
