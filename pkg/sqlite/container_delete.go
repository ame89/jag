package sqlite

import (
	"database/sql"
	"fmt"
)

// ContainerDeleteSummary reports what one DeleteContainers call actually
// removed, so callers (e.g. jaggit's incremental import, see its SPEC.md)
// can log/verify the outcome.
type ContainerDeleteSummary struct {
	Containers int
	Equipment  int
	Edges      int
	Nodes      int
}

// ListContainerIDs returns every model_container.id currently stored.
// Intended for callers that need to diff "containers currently in the DB"
// against "containers still present in some external source" (e.g.
// jaggit's hjson tree, one container per file) to find containers that
// must be removed via DeleteContainers.
func (m *ModelStore) ListContainerIDs() ([]string, error) {
	rows, err := m.db.Query(`SELECT id FROM model_container`)
	if err != nil {
		return nil, fmt.Errorf("sqlite: listing container ids: %w", err)
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("sqlite: scanning container id: %w", err)
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// queryStrings runs query (expected to select exactly one TEXT column)
// and returns every value, or nil if ids is empty (avoids emitting an
// "IN ()" query, which some SQLite versions reject).
func queryStrings(tx *sql.Tx, query string, args []any) ([]string, error) {
	if len(args) == 0 {
		return nil, nil
	}
	rows, err := tx.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// deleteOwnedAttributesAndGeometry removes every model_attribute_value/
// model_geometry_value row owned by any of ownerIDs, resolving each ID to
// its entity_id key via a read-only lookup (never creating a fresh,
// now-orphaned entity_id row for an ID that no longer owns anything —
// see entityIDCache.lookup's doc comment). IDs with no entity_id row at
// all (never had an Attribute/Geometry) are silently skipped.
func deleteOwnedAttributesAndGeometry(tx *sql.Tx, cache *entityIDCache, ownerIDs []string) error {
	for _, id := range ownerIDs {
		key, ok, err := cache.lookup(tx, id)
		if err != nil {
			return fmt.Errorf("sqlite: resolving entity id for %s: %w", id, err)
		}
		if !ok {
			continue
		}
		if _, err := tx.Exec(`DELETE FROM model_attribute_value WHERE owner_id_key = ?`, key); err != nil {
			return fmt.Errorf("sqlite: deleting attributes owned by %s: %w", id, err)
		}
		if _, err := tx.Exec(`DELETE FROM model_geometry_value WHERE owner_id_key = ?`, key); err != nil {
			return fmt.Errorf("sqlite: deleting geometry owned by %s: %w", id, err)
		}
	}
	return nil
}

// DeleteContainers removes containerIDs and everything owned exclusively
// by them (Equipment, Edges, Electrical-Group ownership rows, Attributes,
// Geometry) from the model. Nodes need extra care: a ConnectivityNode can
// legitimately be shared across more than one container/station (see
// modelSchema's model_electrical_group doc comment — confirmed
// cross-station switch coupling in real datasets, e.g.
// ReliCapGrid_Espheim), so a candidate Node is only actually deleted once
// — after this call's own deletions are applied — it has NO remaining
// Edge referencing it, NO remaining Electrical-Group ownership row, and
// NO surviving Equipment row with the same id (a Node-role Equipment like
// BusbarSection/Junction whose container was NOT part of containerIDs).
// A Node is never deleted merely because one of the containers that used
// to reference it is being removed.
//
// This is a genuinely new capability jag did not previously offer:
// existing importers/exporters only ever performed a full rebuild (delete
// the whole SQLite file, re-import everything) — see
// pkg/impl/hjsonimport.Run's doc comment. DeleteContainers instead allows
// a caller to reuse an existing model and remove just the containers that
// disappeared from its source, without discarding the rest of the model.
func (m *ModelStore) DeleteContainers(containerIDs []string) (ContainerDeleteSummary, error) {
	var summary ContainerDeleteSummary
	if len(containerIDs) == 0 {
		return summary, nil
	}
	m.writeMu.Lock()
	defer m.writeMu.Unlock()

	entityCache, err := m.entityIDs()
	if err != nil {
		return summary, err
	}

	err = withTx(m.db, func(tx *sql.Tx) error {
		equipmentIDs, err := queryStrings(tx, fmt.Sprintf(
			`SELECT id FROM model_equipment WHERE container_id IN (%s)`, placeholders(len(containerIDs)),
		), idArgs(containerIDs))
		if err != nil {
			return fmt.Errorf("sqlite: selecting equipment for containers: %w", err)
		}

		// Candidate nodes: edge terminals of these equipment's own edges,
		// nodes owned (as electrical-group perspective) by these
		// containers, and node-role equipment (its id doubles as a
		// model_node id).
		candidateNodes := map[string]struct{}{}
		if len(equipmentIDs) > 0 {
			rows, err := tx.Query(fmt.Sprintf(
				`SELECT terminal1_node_id, terminal2_node_id FROM model_edge WHERE equipment_id IN (%s)`,
				placeholders(len(equipmentIDs)),
			), idArgs(equipmentIDs)...)
			if err != nil {
				return fmt.Errorf("sqlite: selecting edges for containers: %w", err)
			}
			for rows.Next() {
				var n1, n2 string
				if err := rows.Scan(&n1, &n2); err != nil {
					rows.Close()
					return fmt.Errorf("sqlite: scanning edge terminals: %w", err)
				}
				candidateNodes[n1] = struct{}{}
				candidateNodes[n2] = struct{}{}
			}
			if err := rows.Err(); err != nil {
				rows.Close()
				return fmt.Errorf("sqlite: iterating edge terminals: %w", err)
			}
			rows.Close()
			for _, id := range equipmentIDs {
				candidateNodes[id] = struct{}{}
			}
		}
		groupNodes, err := queryStrings(tx, fmt.Sprintf(
			`SELECT DISTINCT node_id FROM model_electrical_group WHERE owner_id IN (%s)`,
			placeholders(len(containerIDs)),
		), idArgs(containerIDs))
		if err != nil {
			return fmt.Errorf("sqlite: selecting electrical group nodes for containers: %w", err)
		}
		for _, n := range groupNodes {
			candidateNodes[n] = struct{}{}
		}

		// Rows owned exclusively by these containers — never shared
		// across containers, safe to remove unconditionally.
		if len(equipmentIDs) > 0 {
			res, err := tx.Exec(fmt.Sprintf(
				`DELETE FROM model_edge WHERE equipment_id IN (%s)`, placeholders(len(equipmentIDs)),
			), idArgs(equipmentIDs)...)
			if err != nil {
				return fmt.Errorf("sqlite: deleting edges: %w", err)
			}
			if n, _ := res.RowsAffected(); n > 0 {
				summary.Edges = int(n)
			}
		}
		if res, err := tx.Exec(fmt.Sprintf(
			`DELETE FROM model_equipment WHERE container_id IN (%s)`, placeholders(len(containerIDs)),
		), idArgs(containerIDs)...); err != nil {
			return fmt.Errorf("sqlite: deleting equipment: %w", err)
		} else if n, _ := res.RowsAffected(); n > 0 {
			summary.Equipment = int(n)
		}
		if _, err := tx.Exec(fmt.Sprintf(
			`DELETE FROM model_electrical_group WHERE owner_id IN (%s)`, placeholders(len(containerIDs)),
		), idArgs(containerIDs)...); err != nil {
			return fmt.Errorf("sqlite: deleting electrical groups: %w", err)
		}
		if res, err := tx.Exec(fmt.Sprintf(
			`DELETE FROM model_container WHERE id IN (%s)`, placeholders(len(containerIDs)),
		), idArgs(containerIDs)...); err != nil {
			return fmt.Errorf("sqlite: deleting containers: %w", err)
		} else if n, _ := res.RowsAffected(); n > 0 {
			summary.Containers = int(n)
		}

		ownedIDs := make([]string, 0, len(containerIDs)+len(equipmentIDs))
		ownedIDs = append(ownedIDs, containerIDs...)
		ownedIDs = append(ownedIDs, equipmentIDs...)
		if err := deleteOwnedAttributesAndGeometry(tx, entityCache, ownedIDs); err != nil {
			return err
		}

		// Orphan check: a candidate Node is only deleted if nothing still
		// references it after the deletions above.
		for nodeID := range candidateNodes {
			var stillReferenced bool
			if err := tx.QueryRow(`
				SELECT EXISTS(
					SELECT 1 FROM model_edge WHERE terminal1_node_id = ?1 OR terminal2_node_id = ?1
					UNION
					SELECT 1 FROM model_electrical_group WHERE node_id = ?1
					UNION
					SELECT 1 FROM model_equipment WHERE id = ?1
				)
			`, nodeID).Scan(&stillReferenced); err != nil {
				return fmt.Errorf("sqlite: checking node %s for remaining references: %w", nodeID, err)
			}
			if stillReferenced {
				continue
			}
			res, err := tx.Exec(`DELETE FROM model_node WHERE id = ?`, nodeID)
			if err != nil {
				return fmt.Errorf("sqlite: deleting orphaned node %s: %w", nodeID, err)
			}
			if n, _ := res.RowsAffected(); n > 0 {
				summary.Nodes++
			}
			if err := deleteOwnedAttributesAndGeometry(tx, entityCache, []string{nodeID}); err != nil {
				return err
			}
			if key, ok, err := entityCache.lookup(tx, nodeID); err != nil {
				return fmt.Errorf("sqlite: resolving entity id for node %s: %w", nodeID, err)
			} else if ok {
				if _, err := tx.Exec(`DELETE FROM model_edge_endpoint WHERE node_id_key = ?`, key); err != nil {
					return fmt.Errorf("sqlite: deleting edge_endpoint rows for node %s: %w", nodeID, err)
				}
			}
		}
		return nil
	})
	return summary, err
}
