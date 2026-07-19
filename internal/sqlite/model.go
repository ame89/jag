// Package sqlite — this file implements the target/final-model storage
// interfaces (internal/core/hierarchy, geometry, topology/physical,
// topology/electrical, technical) on top of SQLite, as a new schema
// alongside the existing Phase 1 staging schema (staging.go) and
// ParameterCatalog schema (catalog.go). Pure persistence only — no
// domain/business logic lives here (see Impl.md, Ports & Adapters).
//
// IMPORTANT (2026-07-14, implementation status): this is a first,
// deliberately NOT-yet-wired-in schema/store implementation (see
// Konzept.md's "DB-Schema für JAG" discussion) — cmd/phase2check and
// internal/impl/common's Phase 2/3 pipeline (BuildNodesAndEdges,
// CheckInvariants, BuildContainers, etc.) still work entirely in-memory
// and do not yet read/write through this store. Wiring the pipeline to
// actually persist here (and, per the still-open "RAM growth" issue,
// batch/stream through it instead of holding full in-memory
// slices/maps) is a separate, larger follow-up step, not done here.
//
// Historisation was dropped entirely (see Konzept.md) — every Upsert here
// overwrites existing rows directly, there is no valid_from/version
// tracking anywhere in this schema.
package sqlite

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	coremodel "gitlab.com/openk-nsc/jag/internal/core/model"
)

// modelSchema creates the final-model tables (Equipment, Node, Edge,
// Container, Geometry, Attribute, electrical group) if they don't exist
// yet. All tables are prefixed model_ to keep them clearly distinguishable
// from staging_* (Phase 1, raw/EAV) and catalog_* (ParameterCatalog) at a
// glance in any DB browser.
const modelSchema = `
CREATE TABLE IF NOT EXISTS model_equipment (
    id           TEXT PRIMARY KEY,
    container_id TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_model_equipment_by_container
    ON model_equipment (container_id);

-- Node.id is NOT always a real Equipment ID: an ordinary ConnectivityNode
-- (not a Node-role Equipment like BusbarSection/Junction) has no
-- corresponding model_equipment row at all (see nodeedge.go's doc comment).
-- Deliberately no FK constraint to model_equipment(id).
CREATE TABLE IF NOT EXISTS model_node (
    id   TEXT PRIMARY KEY,
    kind TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS model_edge (
    equipment_id      TEXT PRIMARY KEY,
    terminal1_node_id TEXT NOT NULL,
    terminal2_node_id TEXT NOT NULL
);

-- Bridge table for GetEdgesByNodeIDs, one row per Edge terminal (2 rows per
-- Edge) — an indexed node_id lookup instead of a
-- "terminal1_node_id IN (...) OR terminal2_node_id IN (...)" join, which
-- can defeat index usage (see Idee.md's graph-traversal performance
-- guidance, explicitly decided before any code existed).
CREATE TABLE IF NOT EXISTS model_edge_endpoint (
    node_id TEXT NOT NULL,
    edge_id TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_model_edge_endpoint_by_node
    ON model_edge_endpoint (node_id);
-- UpsertEdges' delete-then-insert re-upsert strategy runs
-- "DELETE FROM model_edge_endpoint WHERE edge_id = ?" once per Edge;
-- without an index on edge_id this is a full table scan per delete, i.e.
-- O(n^2) for n Edges (found via a lasttest-200 load-test hang: 84600
-- Edges effectively never finished). This index makes that delete a plain
-- indexed lookup, restoring the intended per-Edge O(log n) cost.
CREATE INDEX IF NOT EXISTS idx_model_edge_endpoint_by_edge
    ON model_edge_endpoint (edge_id);

CREATE TABLE IF NOT EXISTS model_container (
    id        TEXT PRIMARY KEY,
    type      TEXT NOT NULL,
    parent_id TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_model_container_by_parent
    ON model_container (parent_id);

CREATE TABLE IF NOT EXISTS model_geometry (
    owner_id   TEXT PRIMARY KEY,
    owner_kind TEXT NOT NULL,
    lat        REAL NOT NULL,
    lon        REAL NOT NULL
);

-- seq disambiguates multi-value keys (see coremodel.Attribute's doc
-- comment) — several rows can legitimately share the same owner_id+key.
CREATE TABLE IF NOT EXISTS model_attribute (
    owner_id TEXT NOT NULL,
    key      TEXT NOT NULL,
    seq      INTEGER NOT NULL,
    value    TEXT NOT NULL,
    PRIMARY KEY (owner_id, key, seq)
);
CREATE INDEX IF NOT EXISTS idx_model_attribute_by_owner
    ON model_attribute (owner_id);

-- owner_id disambiguates independent per-station (or Pass B) perspectives
-- on the SAME raw node_id: a ConnectivityNode legitimately referenced by
-- equipment from more than one station (a real cross-station switch
-- coupling, confirmed in examples/cgmes/ReliCapGrid_Espheim) gets one row
-- PER OWNER instead of a single, arbitrarily-overwritten row — see
-- Konzept.md's "Offene Punkte" for the full write-up of why a single
-- node_id-keyed row was found to be nondeterministic (worker-scheduling-
-- dependent) under concurrent Pass A processing. A node touched by only
-- one station (the overwhelming majority) simply has exactly one row, as
-- before.
CREATE TABLE IF NOT EXISTS model_electrical_group (
    node_id  TEXT NOT NULL,
    owner_id TEXT NOT NULL,
    group_id TEXT NOT NULL,
    PRIMARY KEY (node_id, owner_id)
);
CREATE INDEX IF NOT EXISTS idx_model_electrical_group_by_group
    ON model_electrical_group (group_id);

-- import_flag is a purely EPHEMERAL, import-scoped bookkeeping table (NOT
-- part of the permanent model — see flags.go's doc comment): batches
-- mark "this ID was seen/installed/contained" as they process their own
-- small chunk, so the two whole-model completeness checks
-- ("unreferenced-node", "equipment-without-container", see consistency.go)
-- can run as one paged scan against these small flag rows at the very
-- end of import, instead of holding the whole model's resolved/container
-- maps in RAM at once. version-scoped like every other model_* table;
-- rows are deleted once Phase 3's final scan for that version completes
-- (see ClearFlags) — a fresh import's flags never linger.
CREATE TABLE IF NOT EXISTS import_flag (
    version INTEGER NOT NULL,
    kind    TEXT NOT NULL,
    id      TEXT NOT NULL,
    PRIMARY KEY (version, kind, id)
);
`

// ModelStore implements hierarchy.Store, hierarchy.EquipmentStore,
// geometry.Store, topology/physical.Store, topology/electrical.Store and
// technical.Store on top of a SQLite database. It shares its *sql.DB with a
// StagingStore (see StagingStore.Model), same pattern as CatalogStore.
//
// writeMu serializes every Upsert* call (2026-07-14 fix): SQLite only ever
// allows one writer at a time regardless of WAL mode/busy_timeout, so
// concurrent station workers (see common.BuildSachdatenAndGeometryParallel)
// calling UpsertAttributes/UpsertGeometry concurrently were racing for the
// single write lock and occasionally exceeding even a generous
// busy_timeout under load, surfacing as a hard "SQLITE_BUSY: database is
// locked" error (found via the lasttest-200 load test). A Go-level mutex
// avoids relying on retry/timeout luck entirely — at most one Upsert* call
// executes its transaction at any time, others simply wait on the mutex
// instead of spinning against the database lock. Reads (GetByIDs,
// GetDescendants, ...) are NOT covered by writeMu — SQLite's WAL mode lets
// readers proceed concurrently with a writer.
//
// UPDATE 2026-07-16: writeMu is now a *sync.Mutex pointer, shared with
// every other store sharing this StagingStore's *sql.DB (see
// StagingStore's own writeMu doc comment) — a value-type mutex here would
// only have serialized ModelStore's own Upsert* calls against each other,
// not against FlagStore.MarkFlags, which turned out to race against them
// the same way.
type ModelStore struct {
	db      *sql.DB
	writeMu *sync.Mutex
}

// Model returns a ModelStore sharing this StagingStore's database
// connection (opened once in Open, which also creates the model schema).
func (s *StagingStore) Model() *ModelStore {
	return &ModelStore{db: s.db, writeMu: &s.writeMu}
}

// --- Equipment ---------------------------------------------------------

// GetByIDs implements hierarchy.EquipmentStore.
func (m *ModelStore) GetByIDs(ids []string) ([]coremodel.Equipment, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	args := idArgs(ids)
	rows, err := m.db.Query(fmt.Sprintf(
		`SELECT id, container_id FROM model_equipment WHERE id IN (%s)`,
		placeholders(len(ids)),
	), args...)
	if err != nil {
		return nil, fmt.Errorf("sqlite: querying equipment by id: %w", err)
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

// GetByContainerIDs implements hierarchy.EquipmentStore.
func (m *ModelStore) GetByContainerIDs(containerIDs []string) ([]coremodel.Equipment, error) {
	if len(containerIDs) == 0 {
		return nil, nil
	}
	rows, err := m.db.Query(fmt.Sprintf(
		`SELECT id, container_id FROM model_equipment WHERE container_id IN (%s)`,
		placeholders(len(containerIDs)),
	), idArgs(containerIDs)...)
	if err != nil {
		return nil, fmt.Errorf("sqlite: querying equipment by container id: %w", err)
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

// Upsert implements hierarchy.EquipmentStore.
func (m *ModelStore) UpsertEquipment(equipment []coremodel.Equipment) error {
	if len(equipment) == 0 {
		return nil
	}
	m.writeMu.Lock()
	defer m.writeMu.Unlock()
	return withTx(m.db, func(tx *sql.Tx) error {
		return upsertEquipmentTx(tx, equipment)
	})
}

// upsertEquipmentTx is UpsertEquipment's actual insert body, factored out
// so PersistBatch (below) can run it as one step of a single shared
// transaction spanning an entire Pass A/B batch's writes, instead of each
// entity type opening (and committing) its own transaction.
func upsertEquipmentTx(tx *sql.Tx, equipment []coremodel.Equipment) error {
	if len(equipment) == 0 {
		return nil
	}
	stmt, err := tx.Prepare(`
			INSERT INTO model_equipment (id, container_id) VALUES (?, ?)
			ON CONFLICT (id) DO UPDATE SET container_id = excluded.container_id
		`)
	if err != nil {
		return fmt.Errorf("sqlite: preparing equipment upsert: %w", err)
	}
	defer stmt.Close()

	for _, e := range equipment {
		if _, err := stmt.Exec(e.ID, e.ContainerID); err != nil {
			return fmt.Errorf("sqlite: upserting equipment %s: %w", e.ID, err)
		}
	}
	return nil
}

// --- Container (hierarchy.Store) ---------------------------------------

// ContainerGetByIDs implements hierarchy.Store.GetByIDs. Named distinctly
// from GetByIDs (Equipment's own method above) since a single ModelStore
// implements several Store interfaces whose method names would otherwise
// collide on the same Go type.
func (m *ModelStore) ContainerGetByIDs(ids []string) ([]coremodel.Container, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	rows, err := m.db.Query(fmt.Sprintf(
		`SELECT id, type, parent_id FROM model_container WHERE id IN (%s)`,
		placeholders(len(ids)),
	), idArgs(ids)...)
	if err != nil {
		return nil, fmt.Errorf("sqlite: querying containers by id: %w", err)
	}
	defer rows.Close()
	return scanContainerRows(rows)
}

// GetChildren implements hierarchy.Store.
func (m *ModelStore) GetChildren(parentIDs []string) ([]coremodel.Container, error) {
	if len(parentIDs) == 0 {
		return nil, nil
	}
	rows, err := m.db.Query(fmt.Sprintf(
		`SELECT id, type, parent_id FROM model_container WHERE parent_id IN (%s)`,
		placeholders(len(parentIDs)),
	), idArgs(parentIDs)...)
	if err != nil {
		return nil, fmt.Errorf("sqlite: querying container children: %w", err)
	}
	defer rows.Close()
	return scanContainerRows(rows)
}

// GetDescendants implements hierarchy.Store. Uses a recursive CTE (DB-side
// fixpoint iteration, not Go-side stack recursion — see Idee.md's
// graph-traversal guidance) to walk the parent_id chain downward from
// rootIDs, any depth.
func (m *ModelStore) GetDescendants(rootIDs []string) ([]coremodel.Container, error) {
	if len(rootIDs) == 0 {
		return nil, nil
	}
	query := fmt.Sprintf(`
		WITH RECURSIVE descendants(id, type, parent_id) AS (
			SELECT id, type, parent_id FROM model_container WHERE parent_id IN (%s)
			UNION ALL
			SELECT c.id, c.type, c.parent_id
			FROM model_container c
			JOIN descendants d ON c.parent_id = d.id
		)
		SELECT id, type, parent_id FROM descendants
	`, placeholders(len(rootIDs)))

	rows, err := m.db.Query(query, idArgs(rootIDs)...)
	if err != nil {
		return nil, fmt.Errorf("sqlite: querying container descendants: %w", err)
	}
	defer rows.Close()
	return scanContainerRows(rows)
}

// UpsertContainers implements hierarchy.Store.Upsert. Named distinctly for
// the same reason as ContainerGetByIDs above.
func (m *ModelStore) UpsertContainers(containers []coremodel.Container) error {
	if len(containers) == 0 {
		return nil
	}
	m.writeMu.Lock()
	defer m.writeMu.Unlock()
	return withTx(m.db, func(tx *sql.Tx) error {
		return upsertContainersTx(tx, containers)
	})
}

// upsertContainersTx is UpsertContainers' insert body — see
// upsertEquipmentTx's doc comment for why this is factored out.
func upsertContainersTx(tx *sql.Tx, containers []coremodel.Container) error {
	if len(containers) == 0 {
		return nil
	}
	stmt, err := tx.Prepare(`
			INSERT INTO model_container (id, type, parent_id) VALUES (?, ?, ?)
			ON CONFLICT (id) DO UPDATE SET type = excluded.type, parent_id = excluded.parent_id
		`)
	if err != nil {
		return fmt.Errorf("sqlite: preparing container upsert: %w", err)
	}
	defer stmt.Close()

	for _, c := range containers {
		if _, err := stmt.Exec(c.ID, string(c.Type), c.ParentID); err != nil {
			return fmt.Errorf("sqlite: upserting container %s: %w", c.ID, err)
		}
	}
	return nil
}

// CountByType implements hierarchy.Store. Computed with a single GROUP BY
// query DB-side (see Usecases.md UC12).
func (m *ModelStore) CountByType() (map[string]int, error) {
	rows, err := m.db.Query(`SELECT type, COUNT(*) FROM model_container GROUP BY type`)
	if err != nil {
		return nil, fmt.Errorf("sqlite: querying container counts by type: %w", err)
	}
	defer rows.Close()

	result := map[string]int{}
	for rows.Next() {
		var typ string
		var count int
		if err := rows.Scan(&typ, &count); err != nil {
			return nil, fmt.Errorf("sqlite: scanning container count row: %w", err)
		}
		result[typ] = count
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite: iterating container count rows: %w", err)
	}
	return result, nil
}

func scanContainerRows(rows *sql.Rows) ([]coremodel.Container, error) {
	var result []coremodel.Container
	for rows.Next() {
		var c coremodel.Container
		var typ string
		if err := rows.Scan(&c.ID, &typ, &c.ParentID); err != nil {
			return nil, fmt.Errorf("sqlite: scanning container row: %w", err)
		}
		c.Type = coremodel.ContainerType(typ)
		result = append(result, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite: iterating container rows: %w", err)
	}
	return result, nil
}

// --- Geometry ------------------------------------------------------------

// GetByIDsGeometry implements geometry.Store.GetByIDs (renamed to avoid
// colliding with the Equipment/Container GetByIDs-family methods on the
// same ModelStore type).
func (m *ModelStore) GetByIDsGeometry(ownerIDs []string) ([]coremodel.Geometry, error) {
	if len(ownerIDs) == 0 {
		return nil, nil
	}
	rows, err := m.db.Query(fmt.Sprintf(
		`SELECT owner_id, owner_kind, lat, lon FROM model_geometry WHERE owner_id IN (%s)`,
		placeholders(len(ownerIDs)),
	), idArgs(ownerIDs)...)
	if err != nil {
		return nil, fmt.Errorf("sqlite: querying geometry by owner id: %w", err)
	}
	defer rows.Close()

	var result []coremodel.Geometry
	for rows.Next() {
		var g coremodel.Geometry
		var kind string
		if err := rows.Scan(&g.OwnerID, &kind, &g.Lat, &g.Lon); err != nil {
			return nil, fmt.Errorf("sqlite: scanning geometry row: %w", err)
		}
		g.OwnerKind = coremodel.GeometryOwnerKind(kind)
		result = append(result, g)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite: iterating geometry rows: %w", err)
	}
	return result, nil
}

// InBoundingBox implements geometry.Store — a WGS84 range query (see
// Usecases.md UC3), computed DB-side via a plain indexless range scan
// (model_geometry is small enough per current example datasets that a
// dedicated spatial index isn't warranted yet; revisit if/when real
// multi-GB models make this a bottleneck, per Impl.md's performance
// mandate to keep re-checking such trade-offs).
func (m *ModelStore) InBoundingBox(minLat, minLon, maxLat, maxLon float64) ([]coremodel.Geometry, error) {
	rows, err := m.db.Query(
		`SELECT owner_id, owner_kind, lat, lon FROM model_geometry
		 WHERE lat BETWEEN ? AND ? AND lon BETWEEN ? AND ?`,
		minLat, maxLat, minLon, maxLon,
	)
	if err != nil {
		return nil, fmt.Errorf("sqlite: querying geometry by bounding box: %w", err)
	}
	defer rows.Close()

	var result []coremodel.Geometry
	for rows.Next() {
		var g coremodel.Geometry
		var kind string
		if err := rows.Scan(&g.OwnerID, &kind, &g.Lat, &g.Lon); err != nil {
			return nil, fmt.Errorf("sqlite: scanning geometry row: %w", err)
		}
		g.OwnerKind = coremodel.GeometryOwnerKind(kind)
		result = append(result, g)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite: iterating geometry rows: %w", err)
	}
	return result, nil
}

// UpsertGeometry implements geometry.Store.Upsert.
func (m *ModelStore) UpsertGeometry(geometries []coremodel.Geometry) error {
	if len(geometries) == 0 {
		return nil
	}
	m.writeMu.Lock()
	defer m.writeMu.Unlock()
	return withTx(m.db, func(tx *sql.Tx) error {
		return upsertGeometryTx(tx, geometries)
	})
}

// upsertGeometryTx is UpsertGeometry's insert body — see
// upsertEquipmentTx's doc comment for why this is factored out.
func upsertGeometryTx(tx *sql.Tx, geometries []coremodel.Geometry) error {
	if len(geometries) == 0 {
		return nil
	}
	stmt, err := tx.Prepare(`
			INSERT INTO model_geometry (owner_id, owner_kind, lat, lon) VALUES (?, ?, ?, ?)
			ON CONFLICT (owner_id) DO UPDATE SET
				owner_kind = excluded.owner_kind, lat = excluded.lat, lon = excluded.lon
		`)
	if err != nil {
		return fmt.Errorf("sqlite: preparing geometry upsert: %w", err)
	}
	defer stmt.Close()

	for _, g := range geometries {
		if _, err := stmt.Exec(g.OwnerID, string(g.OwnerKind), g.Lat, g.Lon); err != nil {
			return fmt.Errorf("sqlite: upserting geometry for %s: %w", g.OwnerID, err)
		}
	}
	return nil
}

// --- Physical topology (Node/Edge) ---------------------------------------

// GetNodesByIDs implements topology/physical.Store.
func (m *ModelStore) GetNodesByIDs(ids []string) ([]coremodel.Node, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	rows, err := m.db.Query(fmt.Sprintf(
		`SELECT id, kind FROM model_node WHERE id IN (%s)`,
		placeholders(len(ids)),
	), idArgs(ids)...)
	if err != nil {
		return nil, fmt.Errorf("sqlite: querying nodes by id: %w", err)
	}
	defer rows.Close()

	var result []coremodel.Node
	for rows.Next() {
		var n coremodel.Node
		var kind string
		if err := rows.Scan(&n.EquipmentID, &kind); err != nil {
			return nil, fmt.Errorf("sqlite: scanning node row: %w", err)
		}
		n.Kind = coremodel.NodeKind(kind)
		result = append(result, n)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite: iterating node rows: %w", err)
	}
	return result, nil
}

// GetEdgesByNodeIDs implements topology/physical.Store, backed by the
// model_edge_endpoint bridge table (indexed by node_id) rather than an
// OR-join over terminal1_node_id/terminal2_node_id.
func (m *ModelStore) GetEdgesByNodeIDs(nodeIDs []string) ([]coremodel.Edge, error) {
	if len(nodeIDs) == 0 {
		return nil, nil
	}
	query := fmt.Sprintf(`
		SELECT DISTINCT e.equipment_id, e.terminal1_node_id, e.terminal2_node_id
		FROM model_edge_endpoint ep
		JOIN model_edge e ON e.equipment_id = ep.edge_id
		WHERE ep.node_id IN (%s)
	`, placeholders(len(nodeIDs)))

	rows, err := m.db.Query(query, idArgs(nodeIDs)...)
	if err != nil {
		return nil, fmt.Errorf("sqlite: querying edges by node id: %w", err)
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

// GetEdgesByEquipmentIDs implements topology/physical.Store — a direct
// primary-key lookup on model_edge.equipment_id, as opposed to
// GetEdgesByNodeIDs' node-centric bridge-table lookup.
func (m *ModelStore) GetEdgesByEquipmentIDs(equipmentIDs []string) ([]coremodel.Edge, error) {
	if len(equipmentIDs) == 0 {
		return nil, nil
	}
	rows, err := m.db.Query(fmt.Sprintf(
		`SELECT equipment_id, terminal1_node_id, terminal2_node_id FROM model_edge WHERE equipment_id IN (%s)`,
		placeholders(len(equipmentIDs)),
	), idArgs(equipmentIDs)...)
	if err != nil {
		return nil, fmt.Errorf("sqlite: querying edges by equipment id: %w", err)
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

// GetReachableNodes implements topology/physical.Store via a recursive CTE
// over model_edge_endpoint (DB-side fixpoint iteration, not Go-side
// recursion).
func (m *ModelStore) GetReachableNodes(rootNodeIDs []string) ([]string, error) {
	if len(rootNodeIDs) == 0 {
		return nil, nil
	}
	query := fmt.Sprintf(`
		WITH RECURSIVE reachable(node_id) AS (
			%s
			UNION
			SELECT ep2.node_id
			FROM reachable r
			JOIN model_edge_endpoint ep1 ON ep1.node_id = r.node_id
			JOIN model_edge_endpoint ep2 ON ep2.edge_id = ep1.edge_id
		)
		SELECT node_id FROM reachable
	`, unionAllSelects(len(rootNodeIDs)))

	rows, err := m.db.Query(query, idArgs(rootNodeIDs)...)
	if err != nil {
		return nil, fmt.Errorf("sqlite: querying reachable nodes: %w", err)
	}
	defer rows.Close()

	var result []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("sqlite: scanning reachable node row: %w", err)
		}
		result = append(result, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite: iterating reachable node rows: %w", err)
	}
	return result, nil
}

// UpsertNodes implements topology/physical.Store.
func (m *ModelStore) UpsertNodes(nodes []coremodel.Node) error {
	if len(nodes) == 0 {
		return nil
	}
	m.writeMu.Lock()
	defer m.writeMu.Unlock()
	return withTx(m.db, func(tx *sql.Tx) error {
		return upsertNodesTx(tx, nodes)
	})
}

// upsertNodesTx is UpsertNodes' insert body — see upsertEquipmentTx's doc
// comment for why this is factored out.
func upsertNodesTx(tx *sql.Tx, nodes []coremodel.Node) error {
	if len(nodes) == 0 {
		return nil
	}
	stmt, err := tx.Prepare(`
			INSERT INTO model_node (id, kind) VALUES (?, ?)
			ON CONFLICT (id) DO UPDATE SET kind = excluded.kind
		`)
	if err != nil {
		return fmt.Errorf("sqlite: preparing node upsert: %w", err)
	}
	defer stmt.Close()

	for _, n := range nodes {
		if _, err := stmt.Exec(n.EquipmentID, string(n.Kind)); err != nil {
			return fmt.Errorf("sqlite: upserting node %s: %w", n.EquipmentID, err)
		}
	}
	return nil
}

// UpsertEdges implements topology/physical.Store. Also maintains the
// model_edge_endpoint bridge table: any existing endpoint rows for an
// edge's EquipmentID are replaced (delete-then-insert) so re-upserting an
// Edge with different terminals never leaves stale endpoint rows behind.
func (m *ModelStore) UpsertEdges(edges []coremodel.Edge) error {
	if len(edges) == 0 {
		return nil
	}
	m.writeMu.Lock()
	defer m.writeMu.Unlock()
	return withTx(m.db, func(tx *sql.Tx) error {
		return upsertEdgesTx(tx, edges)
	})
}

// upsertEdgesTx is UpsertEdges' insert body — see upsertEquipmentTx's doc
// comment for why this is factored out.
func upsertEdgesTx(tx *sql.Tx, edges []coremodel.Edge) error {
	if len(edges) == 0 {
		return nil
	}
	edgeStmt, err := tx.Prepare(`
			INSERT INTO model_edge (equipment_id, terminal1_node_id, terminal2_node_id) VALUES (?, ?, ?)
			ON CONFLICT (equipment_id) DO UPDATE SET
				terminal1_node_id = excluded.terminal1_node_id,
				terminal2_node_id = excluded.terminal2_node_id
		`)
	if err != nil {
		return fmt.Errorf("sqlite: preparing edge upsert: %w", err)
	}
	defer edgeStmt.Close()

	deleteEndpointsStmt, err := tx.Prepare(`DELETE FROM model_edge_endpoint WHERE edge_id = ?`)
	if err != nil {
		return fmt.Errorf("sqlite: preparing edge_endpoint delete: %w", err)
	}
	defer deleteEndpointsStmt.Close()

	insertEndpointStmt, err := tx.Prepare(`INSERT INTO model_edge_endpoint (node_id, edge_id) VALUES (?, ?)`)
	if err != nil {
		return fmt.Errorf("sqlite: preparing edge_endpoint insert: %w", err)
	}
	defer insertEndpointStmt.Close()

	for _, e := range edges {
		if _, err := edgeStmt.Exec(e.EquipmentID, e.Terminal1NodeID, e.Terminal2NodeID); err != nil {
			return fmt.Errorf("sqlite: upserting edge %s: %w", e.EquipmentID, err)
		}
		if _, err := deleteEndpointsStmt.Exec(e.EquipmentID); err != nil {
			return fmt.Errorf("sqlite: clearing edge_endpoint rows for %s: %w", e.EquipmentID, err)
		}
		for _, nodeID := range []string{e.Terminal1NodeID, e.Terminal2NodeID} {
			if nodeID == "" {
				continue
			}
			if _, err := insertEndpointStmt.Exec(nodeID, e.EquipmentID); err != nil {
				return fmt.Errorf("sqlite: inserting edge_endpoint for %s/%s: %w", nodeID, e.EquipmentID, err)
			}
		}
	}
	return nil
}

// --- Electrical topology (grouping) --------------------------------------

// GetElectricalGroup implements topology/electrical.Store. A node touched
// by more than one owner (a real cross-station boundary Node, see the
// model_electrical_group DDL comment above) legitimately returns more than
// one group id — callers that need "are these two nodes connected"
// semantics must treat a multi-group node as a union point and expand
// across all of its groups (see usecase.ElectricallyConnected).
func (m *ModelStore) GetElectricalGroup(nodeIDs []string) (map[string][]string, error) {
	if len(nodeIDs) == 0 {
		return nil, nil
	}
	rows, err := m.db.Query(fmt.Sprintf(
		`SELECT node_id, group_id FROM model_electrical_group WHERE node_id IN (%s)`,
		placeholders(len(nodeIDs)),
	), idArgs(nodeIDs)...)
	if err != nil {
		return nil, fmt.Errorf("sqlite: querying electrical group by node id: %w", err)
	}
	defer rows.Close()

	result := map[string][]string{}
	for rows.Next() {
		var nodeID, groupID string
		if err := rows.Scan(&nodeID, &groupID); err != nil {
			return nil, fmt.Errorf("sqlite: scanning electrical group row: %w", err)
		}
		result[nodeID] = append(result[nodeID], groupID)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite: iterating electrical group rows: %w", err)
	}
	return result, nil
}

// GetGroupMembers implements topology/electrical.Store. DISTINCT because a
// boundary node could in principle contribute the identical group_id value
// from two different owners (unlikely given group ids are derived from
// per-owner local computations, but not structurally impossible).
func (m *ModelStore) GetGroupMembers(groupIDs []string) ([]string, error) {
	if len(groupIDs) == 0 {
		return nil, nil
	}
	rows, err := m.db.Query(fmt.Sprintf(
		`SELECT DISTINCT node_id FROM model_electrical_group WHERE group_id IN (%s)`,
		placeholders(len(groupIDs)),
	), idArgs(groupIDs)...)
	if err != nil {
		return nil, fmt.Errorf("sqlite: querying electrical group members: %w", err)
	}
	defer rows.Close()

	var result []string
	for rows.Next() {
		var nodeID string
		if err := rows.Scan(&nodeID); err != nil {
			return nil, fmt.Errorf("sqlite: scanning electrical group member row: %w", err)
		}
		result = append(result, nodeID)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite: iterating electrical group member rows: %w", err)
	}
	return result, nil
}

// GroupSizes implements topology/electrical.Store. Computed with a single
// GROUP BY query DB-side so a caller reporting "N circuits, sizes desc"
// never has to pull every Node ID into Go memory just to count them.
// COUNT(DISTINCT node_id) so a boundary node contributing to the same
// group from two owners isn't double-counted.
func (m *ModelStore) GroupSizes() (map[string]int, error) {
	rows, err := m.db.Query(`SELECT group_id, COUNT(DISTINCT node_id) FROM model_electrical_group GROUP BY group_id`)
	if err != nil {
		return nil, fmt.Errorf("sqlite: querying electrical group sizes: %w", err)
	}
	defer rows.Close()

	result := map[string]int{}
	for rows.Next() {
		var groupID string
		var count int
		if err := rows.Scan(&groupID, &count); err != nil {
			return nil, fmt.Errorf("sqlite: scanning electrical group size row: %w", err)
		}
		result[groupID] = count
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite: iterating electrical group size rows: %w", err)
	}
	return result, nil
}

// UpsertElectricalGroups implements topology/electrical.Store.Upsert
// (renamed from plain Upsert to avoid colliding with the other Upsert*
// methods on this same ModelStore type).
//
// owned is keyed by owner id (a station root Container id for Pass A, or
// the fixed Pass B sentinel owner id — see pass_b.go) mapping to that
// owner's own, independently-computed node_id -> group_id assignment. Each
// owner's contribution is replaced wholesale (delete-then-insert) within
// one transaction, and different owners never touch each other's rows —
// this is what makes concurrent Pass A station workers and Pass B safe to
// run in any order/interleaving without nondeterministic overwrites: a
// station whose local grouping changes (e.g. a switch's default state
// flips between closed and open on re-import, growing or shrinking its
// local group) simply replaces its own prior rows from scratch, and a
// shared boundary node keeps one row per owning station rather than a
// single arbitrarily-overwritten row.
func (m *ModelStore) UpsertElectricalGroups(owned map[string]map[string]string) error {
	if len(owned) == 0 {
		return nil
	}
	m.writeMu.Lock()
	defer m.writeMu.Unlock()
	return withTx(m.db, func(tx *sql.Tx) error {
		return upsertElectricalGroupsTx(tx, owned)
	})
}

// upsertElectricalGroupsTx is UpsertElectricalGroups' insert body — see
// upsertEquipmentTx's doc comment for why this is factored out.
func upsertElectricalGroupsTx(tx *sql.Tx, owned map[string]map[string]string) error {
	if len(owned) == 0 {
		return nil
	}
	deleteStmt, err := tx.Prepare(`DELETE FROM model_electrical_group WHERE owner_id = ?`)
	if err != nil {
		return fmt.Errorf("sqlite: preparing electrical group delete-by-owner: %w", err)
	}
	defer deleteStmt.Close()

	insertStmt, err := tx.Prepare(`INSERT INTO model_electrical_group (node_id, owner_id, group_id) VALUES (?, ?, ?)`)
	if err != nil {
		return fmt.Errorf("sqlite: preparing electrical group insert: %w", err)
	}
	defer insertStmt.Close()

	for owner, groups := range owned {
		if _, err := deleteStmt.Exec(owner); err != nil {
			return fmt.Errorf("sqlite: deleting prior electrical groups for owner %s: %w", owner, err)
		}
		for nodeID, groupID := range groups {
			if _, err := insertStmt.Exec(nodeID, owner, groupID); err != nil {
				return fmt.Errorf("sqlite: inserting electrical group for node %s (owner %s): %w", nodeID, owner, err)
			}
		}
	}
	return nil
}

// --- Sachdaten (Attribute) ------------------------------------------------

// GetByOwnerIDs implements technical.Store.
func (m *ModelStore) GetByOwnerIDs(ownerIDs []string) ([]coremodel.Attribute, error) {
	if len(ownerIDs) == 0 {
		return nil, nil
	}
	rows, err := m.db.Query(fmt.Sprintf(
		`SELECT owner_id, key, value FROM model_attribute WHERE owner_id IN (%s) ORDER BY owner_id, key, seq`,
		placeholders(len(ownerIDs)),
	), idArgs(ownerIDs)...)
	if err != nil {
		return nil, fmt.Errorf("sqlite: querying attributes by owner id: %w", err)
	}
	defer rows.Close()

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

// UpsertAttributes implements technical.Store.Upsert (renamed from plain
// Upsert to avoid colliding with the other Upsert* methods on this same
// ModelStore type). Since several rows can legitimately share the same
// OwnerID+Key (multi-value keys, see coremodel.Attribute's doc comment),
// re-upserting a given OwnerID+Key pair first clears its existing rows
// (delete-then-insert per distinct OwnerID+Key in this batch), then
// inserts the given attributes with a fresh, deterministic seq (by their
// order in the input slice) — this avoids stale leftover rows if an
// owner's attribute count for a key shrinks between imports.
func (m *ModelStore) UpsertAttributes(attributes []coremodel.Attribute) error {
	if len(attributes) == 0 {
		return nil
	}
	m.writeMu.Lock()
	defer m.writeMu.Unlock()
	return withTx(m.db, func(tx *sql.Tx) error {
		return upsertAttributesTx(tx, attributes)
	})
}

// upsertAttributesTx is UpsertAttributes' insert body — see
// upsertEquipmentTx's doc comment for why this is factored out.
func upsertAttributesTx(tx *sql.Tx, attributes []coremodel.Attribute) error {
	if len(attributes) == 0 {
		return nil
	}
	deleteStmt, err := tx.Prepare(`DELETE FROM model_attribute WHERE owner_id = ? AND key = ?`)
	if err != nil {
		return fmt.Errorf("sqlite: preparing attribute delete: %w", err)
	}
	defer deleteStmt.Close()

	insertStmt, err := tx.Prepare(`INSERT INTO model_attribute (owner_id, key, seq, value) VALUES (?, ?, ?, ?)`)
	if err != nil {
		return fmt.Errorf("sqlite: preparing attribute insert: %w", err)
	}
	defer insertStmt.Close()

	type ownerKey struct{ owner, key string }
	cleared := map[ownerKey]bool{}
	seq := map[ownerKey]int{}

	for _, a := range attributes {
		ok := ownerKey{a.OwnerID, string(a.Key)}
		if !cleared[ok] {
			if _, err := deleteStmt.Exec(a.OwnerID, string(a.Key)); err != nil {
				return fmt.Errorf("sqlite: clearing attribute rows for %s.%s: %w", a.OwnerID, a.Key, err)
			}
			cleared[ok] = true
		}
		encoded, err := json.Marshal(a.Value)
		if err != nil {
			return fmt.Errorf("sqlite: encoding attribute value for %s.%s: %w", a.OwnerID, a.Key, err)
		}
		if _, err := insertStmt.Exec(a.OwnerID, string(a.Key), seq[ok], string(encoded)); err != nil {
			return fmt.Errorf("sqlite: upserting attribute %s.%s: %w", a.OwnerID, a.Key, err)
		}
		seq[ok]++
	}
	return nil
}

// PersistBatch implements the modelWriter interface's combined batch
// write (see cmd/phase2check/main.go's modelWriter for the full
// rationale). Unlike internal/postgres, where consolidating 7 network
// round-trip commits into 1 was a genuine measured performance fix,
// SQLite is in-process — but running all 7 entity types inside ONE
// transaction is still strictly better than 7 separate ones (fewer WAL
// fsyncs, and it makes the whole batch atomic: a mid-batch failure no
// longer leaves e.g. Containers committed but Equipment not). This now
// mirrors internal/postgres's PersistBatch structure exactly: one
// writeMu-guarded withTx call running every *Tx helper in sequence.
func (m *ModelStore) PersistBatch(
	containers []coremodel.Container,
	equipment []coremodel.Equipment,
	nodes []coremodel.Node,
	edges []coremodel.Edge,
	attributes []coremodel.Attribute,
	geometries []coremodel.Geometry,
	groups map[string]map[string]string,
) error {
	m.writeMu.Lock()
	defer m.writeMu.Unlock()
	return withTx(m.db, func(tx *sql.Tx) error {
		if err := upsertContainersTx(tx, containers); err != nil {
			return err
		}
		if err := upsertEquipmentTx(tx, equipment); err != nil {
			return err
		}
		if err := upsertNodesTx(tx, nodes); err != nil {
			return err
		}
		if err := upsertEdgesTx(tx, edges); err != nil {
			return err
		}
		if err := upsertAttributesTx(tx, attributes); err != nil {
			return err
		}
		if err := upsertGeometryTx(tx, geometries); err != nil {
			return err
		}
		if len(groups) > 0 {
			if err := upsertElectricalGroupsTx(tx, groups); err != nil {
				return err
			}
		}
		return nil
	})
}

// --- shared helpers --------------------------------------------------------

// withTx runs fn inside a transaction, committing on success and rolling
// back on any error (including a panic re-thrown after rollback).
func withTx(db *sql.DB, fn func(tx *sql.Tx) error) error {
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("sqlite: begin tx: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // no-op if committed

	if err := fn(tx); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("sqlite: committing tx: %w", err)
	}
	return nil
}

// idArgs converts a []string into []any for use as *sql.Rows Query args.
func idArgs(ids []string) []any {
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	return args
}

// unionAllSelects builds "SELECT ? UNION ALL SELECT ? ..." (n terms) for
// use as the non-recursive seed of a "WITH RECURSIVE cte(col) AS (...)"
// query, where the caller-supplied root ID list has to be spliced in as
// bound parameters rather than a literal VALUES row set (found to be
// necessary 2026-07-14: SQLite's modernc.org driver rejected the
// "FROM (VALUES (?), (?)) AS roots(id)" table-alias-with-column-list form
// this was originally written with — a plain UNION ALL of single-column
// SELECTs is more portable and needs no such aliasing).
func unionAllSelects(n int) string {
	terms := make([]string, n)
	for i := range terms {
		terms[i] = "SELECT ?"
	}
	return strings.Join(terms, " UNION ALL ")
}
