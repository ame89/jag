package postgres

import (
	"database/sql"
	"fmt"

	coremodel "github.com/ame89/jag/pkg/core/model"
)

// ContainerDeleteSummary aliases the shared coremodel type — see
// pkg/sqlite/container_delete.go's identical alias and
// pkg/core/model/container_delete.go for the rationale.
type ContainerDeleteSummary = coremodel.ContainerDeleteSummary

// ListContainerIDs returns every model_container.id currently stored. See
// pkg/sqlite/container_delete.go's identical ListContainerIDs for the
// full rationale — this mirrors it query-by-query.
func (m *ModelStore) ListContainerIDs() ([]string, error) {
	rows, err := m.db.Query(`SELECT id FROM model_container`)
	if err != nil {
		return nil, fmt.Errorf("postgres: listing container ids: %w", err)
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("postgres: scanning container id: %w", err)
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// queryStrings runs query (expected to select exactly one TEXT column,
// already rebind()-ed) and returns every value, or nil if args is empty
// (avoids emitting an "IN ()" query).
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
// model_geometry_value row owned by any of ownerIDs — see
// pkg/sqlite/container_delete.go's identical helper for the full
// rationale (resolves each ID to its entity_id key via a read-only
// lookup, never creating a fresh, now-orphaned entity_id row).
func deleteOwnedAttributesAndGeometry(tx *sql.Tx, cache *entityIDCache, ownerIDs []string) error {
	for _, id := range ownerIDs {
		key, ok, err := cache.lookup(tx, id)
		if err != nil {
			return fmt.Errorf("postgres: resolving entity id for %s: %w", id, err)
		}
		if !ok {
			continue
		}
		if _, err := tx.Exec(rebind(`DELETE FROM model_attribute_value WHERE owner_id_key = ?`), key); err != nil {
			return fmt.Errorf("postgres: deleting attributes owned by %s: %w", id, err)
		}
		if _, err := tx.Exec(rebind(`DELETE FROM model_geometry_value WHERE owner_id_key = ?`), key); err != nil {
			return fmt.Errorf("postgres: deleting geometry owned by %s: %w", id, err)
		}
	}
	return nil
}

// DeleteContainers removes containerIDs and everything owned exclusively
// by them (Equipment, Edges, Electrical-Group ownership rows, Attributes,
// Geometry) from the model — see pkg/sqlite/container_delete.go's
// identical DeleteContainers for the full rationale (including the
// orphan-node handling); this mirrors it query-by-query, only wrapping
// every query in rebind() and dropping the writeMu lock (PostgreSQL
// supports genuine concurrent writers, see this package's doc comment).
func (m *ModelStore) DeleteContainers(containerIDs []string) (ContainerDeleteSummary, error) {
	var summary ContainerDeleteSummary
	if len(containerIDs) == 0 {
		return summary, nil
	}

	entityCache, err := m.entityIDs()
	if err != nil {
		return summary, err
	}

	err = withTx(m.db, func(tx *sql.Tx) error {
		equipmentIDs, err := queryStrings(tx, rebind(fmt.Sprintf(
			`SELECT id FROM model_equipment WHERE container_id IN (%s)`, placeholders(len(containerIDs)),
		)), idArgs(containerIDs))
		if err != nil {
			return fmt.Errorf("postgres: selecting equipment for containers: %w", err)
		}

		// Candidate nodes: edge terminals of these equipment's own edges,
		// nodes owned (as electrical-group perspective) by these
		// containers, and node-role equipment (its id doubles as a
		// model_node id).
		candidateNodes := map[string]struct{}{}
		if len(equipmentIDs) > 0 {
			rows, err := tx.Query(rebind(fmt.Sprintf(
				`SELECT terminal1_node_id, terminal2_node_id FROM model_edge WHERE equipment_id IN (%s)`,
				placeholders(len(equipmentIDs)),
			)), idArgs(equipmentIDs)...)
			if err != nil {
				return fmt.Errorf("postgres: selecting edges for containers: %w", err)
			}
			for rows.Next() {
				var n1, n2 string
				if err := rows.Scan(&n1, &n2); err != nil {
					rows.Close()
					return fmt.Errorf("postgres: scanning edge terminals: %w", err)
				}
				candidateNodes[n1] = struct{}{}
				candidateNodes[n2] = struct{}{}
			}
			if err := rows.Err(); err != nil {
				rows.Close()
				return fmt.Errorf("postgres: iterating edge terminals: %w", err)
			}
			rows.Close()
			for _, id := range equipmentIDs {
				candidateNodes[id] = struct{}{}
			}
		}
		groupNodes, err := queryStrings(tx, rebind(fmt.Sprintf(
			`SELECT DISTINCT node_id FROM model_electrical_group WHERE owner_id IN (%s)`,
			placeholders(len(containerIDs)),
		)), idArgs(containerIDs))
		if err != nil {
			return fmt.Errorf("postgres: selecting electrical group nodes for containers: %w", err)
		}
		for _, n := range groupNodes {
			candidateNodes[n] = struct{}{}
		}

		// Rows owned exclusively by these containers — never shared
		// across containers, safe to remove unconditionally.
		if len(equipmentIDs) > 0 {
			res, err := tx.Exec(rebind(fmt.Sprintf(
				`DELETE FROM model_edge WHERE equipment_id IN (%s)`, placeholders(len(equipmentIDs)),
			)), idArgs(equipmentIDs)...)
			if err != nil {
				return fmt.Errorf("postgres: deleting edges: %w", err)
			}
			if n, _ := res.RowsAffected(); n > 0 {
				summary.Edges = int(n)
			}
		}
		if res, err := tx.Exec(rebind(fmt.Sprintf(
			`DELETE FROM model_equipment WHERE container_id IN (%s)`, placeholders(len(containerIDs)),
		)), idArgs(containerIDs)...); err != nil {
			return fmt.Errorf("postgres: deleting equipment: %w", err)
		} else if n, _ := res.RowsAffected(); n > 0 {
			summary.Equipment = int(n)
		}
		if _, err := tx.Exec(rebind(fmt.Sprintf(
			`DELETE FROM model_electrical_group WHERE owner_id IN (%s)`, placeholders(len(containerIDs)),
		)), idArgs(containerIDs)...); err != nil {
			return fmt.Errorf("postgres: deleting electrical groups: %w", err)
		}
		if res, err := tx.Exec(rebind(fmt.Sprintf(
			`DELETE FROM model_container WHERE id IN (%s)`, placeholders(len(containerIDs)),
		)), idArgs(containerIDs)...); err != nil {
			return fmt.Errorf("postgres: deleting containers: %w", err)
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
			if err := tx.QueryRow(rebind(`
				SELECT EXISTS(
					SELECT 1 FROM model_edge WHERE terminal1_node_id = ? OR terminal2_node_id = ?
					UNION
					SELECT 1 FROM model_electrical_group WHERE node_id = ?
					UNION
					SELECT 1 FROM model_equipment WHERE id = ?
				)
			`), nodeID, nodeID, nodeID, nodeID).Scan(&stillReferenced); err != nil {
				return fmt.Errorf("postgres: checking node %s for remaining references: %w", nodeID, err)
			}
			if stillReferenced {
				continue
			}
			res, err := tx.Exec(rebind(`DELETE FROM model_node WHERE id = ?`), nodeID)
			if err != nil {
				return fmt.Errorf("postgres: deleting orphaned node %s: %w", nodeID, err)
			}
			if n, _ := res.RowsAffected(); n > 0 {
				summary.Nodes++
			}
			if err := deleteOwnedAttributesAndGeometry(tx, entityCache, []string{nodeID}); err != nil {
				return err
			}
			if key, ok, err := entityCache.lookup(tx, nodeID); err != nil {
				return fmt.Errorf("postgres: resolving entity id for node %s: %w", nodeID, err)
			} else if ok {
				if _, err := tx.Exec(rebind(`DELETE FROM model_edge_endpoint WHERE node_id_key = ?`), key); err != nil {
					return fmt.Errorf("postgres: deleting edge_endpoint rows for node %s: %w", nodeID, err)
				}
			}
		}
		return nil
	})
	return summary, err
}
