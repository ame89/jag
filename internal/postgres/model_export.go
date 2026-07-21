package postgres

import (
	"database/sql"
	"encoding/json"
	"fmt"

	coremodel "gitlab.com/openk-nsc/jag/internal/core/model"
)

// This file adds small, cursor-paginated "read the whole model" bulk
// methods to ModelStore, needed by internal/exporter/hjson (the Fachmodell
// HJSON exporter) — see internal/sqlite/model_export.go for the full
// rationale this mirrors query-by-query (only rebind(...) wrapping
// differs).

// AllContainers pages through every Container in ID order.
func (m *ModelStore) AllContainers(afterID string, limit int) ([]coremodel.Container, error) {
	rows, err := m.db.Query(
		rebind(`SELECT id, type, parent_id FROM model_container WHERE id > ? ORDER BY id LIMIT ?`),
		afterID, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("postgres: paging containers: %w", err)
	}
	defer rows.Close()
	return scanContainerRows(rows)
}

// AllEquipment pages through every Equipment in ID order.
func (m *ModelStore) AllEquipment(afterID string, limit int) ([]coremodel.Equipment, error) {
	rows, err := m.db.Query(
		rebind(`SELECT id, container_id FROM model_equipment WHERE id > ? ORDER BY id LIMIT ?`),
		afterID, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("postgres: paging equipment: %w", err)
	}
	defer rows.Close()

	var result []coremodel.Equipment
	for rows.Next() {
		var e coremodel.Equipment
		if err := rows.Scan(&e.ID, &e.ContainerID); err != nil {
			return nil, fmt.Errorf("postgres: scanning equipment row: %w", err)
		}
		result = append(result, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres: iterating equipment rows: %w", err)
	}
	return result, nil
}

// AllEdges pages through every Edge in equipment-ID order.
func (m *ModelStore) AllEdges(afterID string, limit int) ([]coremodel.Edge, error) {
	rows, err := m.db.Query(
		rebind(`SELECT equipment_id, terminal1_node_id, terminal2_node_id FROM model_edge WHERE equipment_id > ? ORDER BY equipment_id LIMIT ?`),
		afterID, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("postgres: paging edges: %w", err)
	}
	defer rows.Close()

	var result []coremodel.Edge
	for rows.Next() {
		var e coremodel.Edge
		if err := rows.Scan(&e.EquipmentID, &e.Terminal1NodeID, &e.Terminal2NodeID); err != nil {
			return nil, fmt.Errorf("postgres: scanning edge row: %w", err)
		}
		result = append(result, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres: iterating edge rows: %w", err)
	}
	return result, nil
}

// AllAttributes pages through every Attribute in (owner_id, key, seq)
// order — same decoding as GetByOwnerIDs. See
// internal/sqlite/model_export.go's AllAttributes doc comment for the
// 2026-07-21 owner-straddling-page-boundary bugfix this mirrors: a page
// is never allowed to end mid-owner, so a trailing owner's remaining
// rows are fetched via a follow-up query instead of being silently
// dropped by the next call's owner_id cursor.
func (m *ModelStore) AllAttributes(afterOwnerID string, limit int) ([]coremodel.Attribute, error) {
	rows, err := m.db.Query(
		rebind(`SELECT owner_id, key, seq, value FROM model_attribute WHERE owner_id > ? ORDER BY owner_id, key, seq LIMIT ?`),
		afterOwnerID, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("postgres: paging attributes: %w", err)
	}
	result, lastKey, lastSeq, err := scanAttributeRowsWithCursor(rows)
	rows.Close()
	if err != nil {
		return nil, err
	}

	for len(result) > 0 && len(result) >= limit {
		lastOwner := result[len(result)-1].OwnerID
		moreRows, err := m.db.Query(
			rebind(`SELECT owner_id, key, seq, value FROM model_attribute WHERE owner_id = ? AND (key, seq) > (?, ?) ORDER BY key, seq LIMIT ?`),
			lastOwner, lastKey, lastSeq, limit,
		)
		if err != nil {
			return nil, fmt.Errorf("postgres: paging attributes (owner continuation): %w", err)
		}
		more, newLastKey, newLastSeq, err := scanAttributeRowsWithCursor(moreRows)
		moreRows.Close()
		if err != nil {
			return nil, err
		}
		if len(more) == 0 {
			break
		}
		result = append(result, more...)
		lastKey, lastSeq = newLastKey, newLastSeq
		if len(more) < limit {
			break
		}
	}

	return result, nil
}

// AllGeometry pages through every Geometry in owner-ID order.
func (m *ModelStore) AllGeometry(afterOwnerID string, limit int) ([]coremodel.Geometry, error) {
	rows, err := m.db.Query(
		rebind(`SELECT owner_id, owner_kind, lat, lon FROM model_geometry WHERE owner_id > ? ORDER BY owner_id LIMIT ?`),
		afterOwnerID, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("postgres: paging geometry: %w", err)
	}
	defer rows.Close()

	var result []coremodel.Geometry
	for rows.Next() {
		var g coremodel.Geometry
		var ownerKind string
		if err := rows.Scan(&g.OwnerID, &ownerKind, &g.Lat, &g.Lon); err != nil {
			return nil, fmt.Errorf("postgres: scanning geometry row: %w", err)
		}
		g.OwnerKind = coremodel.GeometryOwnerKind(ownerKind)
		result = append(result, g)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres: iterating geometry rows: %w", err)
	}
	return result, nil
}

// scanAttributeRowsWithCursor decodes rows produced by a "SELECT owner_id,
// key, seq, value FROM model_attribute ..." query (like scanAttributeRows,
// but also tracking the last row's key/seq so AllAttributes' owner-
// continuation query above can resume exactly where this page left off).
func scanAttributeRowsWithCursor(rows *sql.Rows) ([]coremodel.Attribute, string, int, error) {
	var result []coremodel.Attribute
	var lastKey string
	var lastSeq int
	for rows.Next() {
		var ownerID, key, rawValue string
		var seq int
		if err := rows.Scan(&ownerID, &key, &seq, &rawValue); err != nil {
			return nil, "", 0, fmt.Errorf("postgres: scanning attribute row: %w", err)
		}
		var value any
		if err := json.Unmarshal([]byte(rawValue), &value); err != nil {
			return nil, "", 0, fmt.Errorf("postgres: decoding attribute value for %s.%s: %w", ownerID, key, err)
		}
		result = append(result, coremodel.Attribute{
			OwnerID: ownerID,
			Key:     coremodel.AttributeKey(key),
			Value:   value,
		})
		lastKey, lastSeq = key, seq
	}
	if err := rows.Err(); err != nil {
		return nil, "", 0, fmt.Errorf("postgres: iterating attribute rows: %w", err)
	}
	return result, lastKey, lastSeq, nil
}

// scanAttributeRows decodes rows produced by a "SELECT owner_id, key,
// value FROM model_attribute ..." query — shared by GetByOwnerIDs (model.go)
// and AllAttributes above.
func scanAttributeRows(rows *sql.Rows) ([]coremodel.Attribute, error) {
	var result []coremodel.Attribute
	for rows.Next() {
		var ownerID, key, rawValue string
		if err := rows.Scan(&ownerID, &key, &rawValue); err != nil {
			return nil, fmt.Errorf("postgres: scanning attribute row: %w", err)
		}
		var value any
		if err := json.Unmarshal([]byte(rawValue), &value); err != nil {
			return nil, fmt.Errorf("postgres: decoding attribute value for %s.%s: %w", ownerID, key, err)
		}
		result = append(result, coremodel.Attribute{
			OwnerID: ownerID,
			Key:     coremodel.AttributeKey(key),
			Value:   value,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres: iterating attribute rows: %w", err)
	}
	return result, nil
}
