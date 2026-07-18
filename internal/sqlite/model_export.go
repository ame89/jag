package sqlite

import (
	"database/sql"
	"encoding/json"
	"fmt"

	coremodel "gitlab.com/openk-nsc/jag/internal/core/model"
)

// This file adds small, cursor-paginated "read the whole model" bulk
// methods to ModelStore, needed by internal/exporter/hjson (the Fachmodell
// HJSON exporter): unlike every other query in this package (which reads a
// bounded set of IDs the caller already knows), an exporter genuinely
// needs to enumerate an entire persisted model — no existing
// hierarchy/technical/topology interface has a "get all" method (only
// ID-based lookups and CountByType). Following the same chunked-cursor
// shape as staging.Store.GetByClass (afterID/limit) keeps RAM bounded per
// call regardless of model size — the caller is expected to page through
// with AllX(lastSeenID, limit) until fewer than limit rows come back,
// exactly like GetByClass's callers already do.

// AllContainers pages through every Container in ID order.
func (m *ModelStore) AllContainers(afterID string, limit int) ([]coremodel.Container, error) {
	rows, err := m.db.Query(
		`SELECT id, type, parent_id FROM model_container WHERE id > ? ORDER BY id LIMIT ?`,
		afterID, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("sqlite: paging containers: %w", err)
	}
	defer rows.Close()
	return scanContainerRows(rows)
}

// AllEquipment pages through every Equipment in ID order.
func (m *ModelStore) AllEquipment(afterID string, limit int) ([]coremodel.Equipment, error) {
	rows, err := m.db.Query(
		`SELECT id, container_id FROM model_equipment WHERE id > ? ORDER BY id LIMIT ?`,
		afterID, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("sqlite: paging equipment: %w", err)
	}
	defer rows.Close()

	var result []coremodel.Equipment
	for rows.Next() {
		var e coremodel.Equipment
		if err := rows.Scan(&e.ID, &e.ContainerID); err != nil {
			return nil, fmt.Errorf("sqlite: scanning equipment row: %w", err)
		}
		result = append(result, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite: iterating equipment rows: %w", err)
	}
	return result, nil
}

// AllEdges pages through every Edge in equipment-ID order.
func (m *ModelStore) AllEdges(afterID string, limit int) ([]coremodel.Edge, error) {
	rows, err := m.db.Query(
		`SELECT equipment_id, terminal1_node_id, terminal2_node_id FROM model_edge WHERE equipment_id > ? ORDER BY equipment_id LIMIT ?`,
		afterID, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("sqlite: paging edges: %w", err)
	}
	defer rows.Close()

	var result []coremodel.Edge
	for rows.Next() {
		var e coremodel.Edge
		if err := rows.Scan(&e.EquipmentID, &e.Terminal1NodeID, &e.Terminal2NodeID); err != nil {
			return nil, fmt.Errorf("sqlite: scanning edge row: %w", err)
		}
		result = append(result, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite: iterating edge rows: %w", err)
	}
	return result, nil
}

// AllAttributes pages through every Attribute in (owner_id, key, seq)
// order — same decoding as GetByOwnerIDs.
func (m *ModelStore) AllAttributes(afterOwnerID string, limit int) ([]coremodel.Attribute, error) {
	rows, err := m.db.Query(
		`SELECT owner_id, key, value FROM model_attribute WHERE owner_id > ? ORDER BY owner_id, key, seq LIMIT ?`,
		afterOwnerID, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("sqlite: paging attributes: %w", err)
	}
	defer rows.Close()
	return scanAttributeRows(rows)
}

// AllGeometry pages through every Geometry in owner-ID order — added
// 2026-07-19 for the HJSON Fachmodell exporter's container/equipment
// coordinate export (see internal/exporter/hjson/model.go's Snapshot),
// following the exact same cursor-pagination shape as AllContainers/
// AllEquipment/AllEdges/AllAttributes above.
func (m *ModelStore) AllGeometry(afterOwnerID string, limit int) ([]coremodel.Geometry, error) {
	rows, err := m.db.Query(
		`SELECT owner_id, owner_kind, lat, lon FROM model_geometry WHERE owner_id > ? ORDER BY owner_id LIMIT ?`,
		afterOwnerID, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("sqlite: paging geometry: %w", err)
	}
	defer rows.Close()

	var result []coremodel.Geometry
	for rows.Next() {
		var g coremodel.Geometry
		var ownerKind string
		if err := rows.Scan(&g.OwnerID, &ownerKind, &g.Lat, &g.Lon); err != nil {
			return nil, fmt.Errorf("sqlite: scanning geometry row: %w", err)
		}
		g.OwnerKind = coremodel.GeometryOwnerKind(ownerKind)
		result = append(result, g)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite: iterating geometry rows: %w", err)
	}
	return result, nil
}

// scanAttributeRows decodes rows produced by a "SELECT owner_id, key,
// value FROM model_attribute ..." query — factored out of GetByOwnerIDs so
// AllAttributes above can share the exact same JSON-decoding behavior.
func scanAttributeRows(rows *sql.Rows) ([]coremodel.Attribute, error) {
	var result []coremodel.Attribute
	for rows.Next() {
		var ownerID, key, rawValue string
		if err := rows.Scan(&ownerID, &key, &rawValue); err != nil {
			return nil, fmt.Errorf("sqlite: scanning attribute row: %w", err)
		}
		var value any
		if err := json.Unmarshal([]byte(rawValue), &value); err != nil {
			return nil, fmt.Errorf("sqlite: decoding attribute value for %s.%s: %w", ownerID, key, err)
		}
		result = append(result, coremodel.Attribute{
			OwnerID: ownerID,
			Key:     coremodel.AttributeKey(key),
			Value:   value,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite: iterating attribute rows: %w", err)
	}
	return result, nil
}
