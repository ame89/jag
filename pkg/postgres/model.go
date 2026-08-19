// Package postgres — this file implements the target/final-model storage
// interfaces (internal/core/hierarchy, geometry, topology/physical,
// topology/electrical, technical) on top of PostgreSQL, mirroring
// internal/sqlite/model.go query-by-query. See this package's doc comment
// (postgres.go) for the overall parity rationale and rebind.go for how
// the shared "?" placeholder style is translated to "$N".
//
// Historisation was dropped entirely (see Konzept.md) — every Upsert here
// overwrites existing rows directly, there is no valid_from/version
// tracking anywhere in this schema.
package postgres

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	coremodel "github.com/ame89/jag/pkg/core/model"
)

// modelSchema creates the final-model tables (Equipment, Node, Edge,
// Container, Geometry, Attribute, electrical group) if they don't exist
// yet. All tables are prefixed model_ to keep them clearly distinguishable
// from staging_* (Phase 1, raw/EAV) and catalog_* (ParameterCatalog) at a
// glance in any DB browser.
//
// Differences from internal/sqlite/model.go's identical schema: REAL ->
// DOUBLE PRECISION (PostgreSQL's REAL is single-precision float4, but
// coremodel.Geometry's Lat/Lon are Go float64 — DOUBLE PRECISION is
// PostgreSQL's float8, the correct match); BIGINT instead of INTEGER for
// version/seq columns that hold a uint64 Go value (SQLite has no fixed
// integer width, so INTEGER there silently accepted any size — PostgreSQL
// does distinguish INTEGER (32-bit) from BIGINT (64-bit), and this
// codebase's version counters are typed uint64 in Go).
const modelSchema = `
CREATE TABLE IF NOT EXISTS model_equipment (
    id           TEXT PRIMARY KEY,
    container_id TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_model_equipment_by_container
    ON model_equipment (container_id);

-- Node.id is NOT always a real Equipment ID: an ordinary ConnectivityNode
-- (not a Node-role Equipment like BusbarSection/Junction) has no
-- corresponding model_equipment row at all (see nodeedge.go's doc comment
-- in internal/impl/common). Deliberately no FK constraint to
-- model_equipment(id).
CREATE TABLE IF NOT EXISTS model_node (
    id   TEXT PRIMARY KEY,
    kind TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS model_edge (
    equipment_id      TEXT PRIMARY KEY,
    terminal1_node_id TEXT NOT NULL,
    terminal2_node_id TEXT NOT NULL
);

-- entity_id is the (large, ever-growing) dictionary of every distinct
-- Node/Edge/Container/Equipment TEXT ID ever seen — see pkg/sqlite/
-- model.go's identical schema comment and entity_id.go for the full
-- rationale (lazy get-or-create; scoped only to model_edge_endpoint/
-- model_attribute_value/model_geometry, the three tables explicitly
-- identified as the DB's biggest size drivers).
CREATE TABLE IF NOT EXISTS entity_id (
    id          BIGSERIAL PRIMARY KEY,
    external_id TEXT NOT NULL UNIQUE
);

-- Bridge table for GetEdgesByNodeIDs, one row per Edge terminal (2 rows per
-- Edge) — an indexed node_id lookup instead of a
-- "terminal1_node_id IN (...) OR terminal2_node_id IN (...)" join, which
-- can defeat index usage (see Idee.md's graph-traversal performance
-- guidance, explicitly decided before any code existed). node_id_key/
-- edge_id_key reference entity_id(id) instead of storing the raw TEXT id
-- (see entity_id.go) — this table has no external readers outside
-- model.go, so no compatibility VIEW is needed.
CREATE TABLE IF NOT EXISTS model_edge_endpoint (
    node_id_key BIGINT NOT NULL REFERENCES entity_id(id),
    edge_id_key BIGINT NOT NULL REFERENCES entity_id(id)
);
CREATE INDEX IF NOT EXISTS idx_model_edge_endpoint_by_node
    ON model_edge_endpoint (node_id_key);
CREATE INDEX IF NOT EXISTS idx_model_edge_endpoint_by_edge
    ON model_edge_endpoint (edge_id_key);

CREATE TABLE IF NOT EXISTS model_container (
    id        TEXT PRIMARY KEY,
    type      TEXT NOT NULL,
    parent_id TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_model_container_by_parent
    ON model_container (parent_id);

-- Physical storage keyed by entity_id (see entity_id.go) instead of the
-- raw TEXT owner_id; the model_geometry VIEW below resolves owner_id_key
-- back to the TEXT id so every existing reader keeps seeing the exact
-- same (owner_id, owner_kind, lat, lon) shape without any change.
CREATE TABLE IF NOT EXISTS model_geometry_value (
    owner_id_key BIGINT PRIMARY KEY REFERENCES entity_id(id),
    owner_kind   TEXT NOT NULL,
    lat          DOUBLE PRECISION NOT NULL,
    lon          DOUBLE PRECISION NOT NULL
);

-- Read-only compatibility view — see pkg/sqlite/model.go's identical view
-- for the full rationale. Writes must go through model_geometry_value
-- (via entityIDCache-resolved owner_id_key) instead. PostgreSQL has no
-- "CREATE VIEW IF NOT EXISTS"; CREATE OR REPLACE VIEW is the idempotent
-- equivalent (safe to run on every Open).
CREATE OR REPLACE VIEW model_geometry AS
SELECT e.external_id AS owner_id, g.owner_kind, g.lat, g.lon
FROM model_geometry_value g
JOIN entity_id e ON e.id = g.owner_id_key;

-- attribute_key is the (small, slowly-growing, curated) dictionary of
-- every distinct Sachdaten key name ever seen — see internal/sqlite/
-- model.go's identical schema comment and attribute_key.go for the full
-- rationale (lazy get-or-create, not a pre-seeded fixed enum).
CREATE TABLE IF NOT EXISTS attribute_key (
    id   BIGSERIAL PRIMARY KEY,
    name TEXT NOT NULL UNIQUE
);

-- seq disambiguates multi-value keys (see coremodel.Attribute's doc
-- comment) — several rows can legitimately share the same owner_id+key_id.
-- Physical storage lives here, keyed by entity_id (see entity_id.go)
-- instead of the raw TEXT owner_id; the model_attribute VIEW below
-- resolves both owner_id_key and key_id back to their TEXT/name form so
-- every existing reader keeps seeing the exact same
-- (owner_id, key, seq, value) shape without any change.
CREATE TABLE IF NOT EXISTS model_attribute_value (
    owner_id_key BIGINT NOT NULL REFERENCES entity_id(id),
    key_id       BIGINT NOT NULL REFERENCES attribute_key(id),
    seq          INTEGER NOT NULL,
    value        TEXT NOT NULL,
    PRIMARY KEY (owner_id_key, key_id, seq)
);
CREATE INDEX IF NOT EXISTS idx_model_attribute_value_by_owner
    ON model_attribute_value (owner_id_key);

-- Read-only compatibility view — see internal/sqlite/model.go's identical
-- view for the full rationale. Writes must go through
-- model_attribute_value (via attributeKeyCache/entityIDCache-resolved
-- key_id/owner_id_key) instead.
-- PostgreSQL has no "CREATE VIEW IF NOT EXISTS"; CREATE OR REPLACE VIEW is
-- the idempotent equivalent (safe to run on every Open, unlike a plain
-- CREATE VIEW which would error on the second run).
CREATE OR REPLACE VIEW model_attribute AS
SELECT e.external_id AS owner_id, k.name AS key, v.seq, v.value
FROM model_attribute_value v
JOIN attribute_key k ON k.id = v.key_id
JOIN entity_id e ON e.id = v.owner_id_key;

-- owner_id disambiguates independent per-station (or Pass B) perspectives
-- on the SAME raw node_id — see internal/sqlite/model.go's identical
-- schema comment for the full rationale (a real cross-station boundary
-- Node legitimately belongs to more than one owner's electrical group).
CREATE TABLE IF NOT EXISTS model_electrical_group (
    node_id  TEXT NOT NULL,
    owner_id TEXT NOT NULL,
    group_id TEXT NOT NULL,
    PRIMARY KEY (node_id, owner_id)
);
CREATE INDEX IF NOT EXISTS idx_model_electrical_group_by_group
    ON model_electrical_group (group_id);

-- import_flag is a purely EPHEMERAL, import-scoped bookkeeping table (NOT
-- part of the permanent model — see flags.go's doc comment): batches mark
-- "this ID was seen/installed/contained" as they process their own small
-- chunk, so the two whole-model completeness checks can run as one paged
-- scan against these small flag rows at the very end of import, instead of
-- holding the whole model's resolved/container maps in RAM at once.
CREATE TABLE IF NOT EXISTS import_flag (
    version BIGINT NOT NULL,
    kind    TEXT NOT NULL,
    id      TEXT NOT NULL,
    PRIMARY KEY (version, kind, id)
);
`

// ModelStore implements hierarchy.Store, hierarchy.EquipmentStore,
// geometry.Store, topology/physical.Store, topology/electrical.Store and
// technical.Store on top of a PostgreSQL database. It shares its *sql.DB
// with a StagingStore (see StagingStore.Model), same pattern as
// CatalogStore.
//
// No writeMu here (unlike internal/sqlite): PostgreSQL supports genuine
// concurrent writers (MVCC + row-level locking), and a real
// lasttest-200-10-10 load-test measurement showed the shared mutex
// serializing every Pass A/B worker's PersistBatch call onto a single
// writer, which is an actual measured bottleneck for this backend
// specifically (not just kept-for-parity speculation). The delete-then-
// insert re-upsert pattern (UpsertAttributes, UpsertElectricalGroups,
// UpsertEdges' endpoint maintenance) is safe without the mutex as long as
// Pass A/B callers never concurrently touch the same owner/key across two
// workers — an invariant already relied upon elsewhere (see
// internal/impl/common) and unaffected by removing this lock.
type ModelStore struct {
	db *sql.DB

	keyCacheOnce sync.Once
	keyCache     *attributeKeyCache
	keyCacheErr  error

	entityCacheOnce sync.Once
	entityCache     *entityIDCache
	entityCacheErr  error
}

// Model returns a ModelStore sharing this StagingStore's database
// connection (opened once in Open, which also creates the model schema).
func (s *StagingStore) Model() *ModelStore {
	return &ModelStore{db: s.db}
}

// attributeKeys returns this ModelStore's attributeKeyCache, loading it
// from the attribute_key table on first use — see internal/sqlite/
// model.go's identical attributeKeys() for the rationale (lazy, not
// eager in Model(), since Model() has no error return).
func (m *ModelStore) attributeKeys() (*attributeKeyCache, error) {
	m.keyCacheOnce.Do(func() {
		m.keyCache, m.keyCacheErr = loadAttributeKeyCache(m.db)
	})
	return m.keyCache, m.keyCacheErr
}

// entityIDs returns this ModelStore's entityIDCache, loading it from the
// entity_id table on first use — same lazy rationale as attributeKeys()
// above.
func (m *ModelStore) entityIDs() (*entityIDCache, error) {
	m.entityCacheOnce.Do(func() {
		m.entityCache, m.entityCacheErr = loadEntityIDCache(m.db)
	})
	return m.entityCache, m.entityCacheErr
}

// --- Equipment ---------------------------------------------------------

// GetByIDs implements hierarchy.EquipmentStore.
func (m *ModelStore) GetByIDs(ids []string) ([]coremodel.Equipment, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	args := idArgs(ids)
	rows, err := m.db.Query(rebind(fmt.Sprintf(
		`SELECT id, container_id FROM model_equipment WHERE id IN (%s)`,
		placeholders(len(ids)),
	)), args...)
	if err != nil {
		return nil, fmt.Errorf("postgres: querying equipment by id: %w", err)
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

// GetByContainerIDs implements hierarchy.EquipmentStore.
func (m *ModelStore) GetByContainerIDs(containerIDs []string) ([]coremodel.Equipment, error) {
	if len(containerIDs) == 0 {
		return nil, nil
	}
	rows, err := m.db.Query(rebind(fmt.Sprintf(
		`SELECT id, container_id FROM model_equipment WHERE container_id IN (%s)`,
		placeholders(len(containerIDs)),
	)), idArgs(containerIDs)...)
	if err != nil {
		return nil, fmt.Errorf("postgres: querying equipment by container id: %w", err)
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

// dedupeLast returns items with duplicate keys collapsed, keeping only the
// last occurrence of each key (relative order of first appearance
// otherwise preserved). This is required before any chunked multi-row
// `INSERT ... ON CONFLICT (...) DO UPDATE`: unlike SQLite's per-row Exec
// loop (which happily re-executes the same upsert twice for a repeated
// key) or SQLite's "INSERT OR REPLACE", PostgreSQL rejects a single
// statement whose VALUES list would update the same conflicting row more
// than once ("ON CONFLICT DO UPDATE command cannot affect row a second
// time", SQLSTATE 21000) — this is a real, observed failure from
// lasttest-200-10-10 (a shared Node touched by two Equipment landed
// twice in one Pass A batch's node upsert chunk). Keeping the last
// occurrence matches the original per-row loop's semantics (last write
// wins).
func dedupeLast[T any](items []T, key func(T) string) []T {
	if len(items) < 2 {
		return items
	}
	lastIdx := make(map[string]int, len(items))
	order := make([]string, 0, len(items))
	for i, it := range items {
		k := key(it)
		if _, ok := lastIdx[k]; !ok {
			order = append(order, k)
		}
		lastIdx[k] = i
	}
	if len(order) == len(items) {
		return items // no duplicates found, avoid an unnecessary copy
	}
	result := make([]T, 0, len(order))
	for _, k := range order {
		result = append(result, items[lastIdx[k]])
	}
	return result
}

// UpsertEquipment implements hierarchy.EquipmentStore.Upsert.
func (m *ModelStore) UpsertEquipment(equipment []coremodel.Equipment) error {
	if len(equipment) == 0 {
		return nil
	}
	return withTx(m.db, func(tx *sql.Tx) error {
		return upsertEquipmentTx(tx, equipment)
	})
}

// upsertEquipmentTx is UpsertEquipment's actual chunked-insert body,
// factored out so PersistBatch (below) can run it as one step of a single
// shared transaction spanning an entire Pass A/B batch's writes, instead
// of each entity type opening (and committing/fsync-ing) its own
// transaction. See PersistBatch's doc comment for the full rationale.
func upsertEquipmentTx(tx *sql.Tx, equipment []coremodel.Equipment) error {
	if len(equipment) == 0 {
		return nil
	}
	equipment = dedupeLast(equipment, func(e coremodel.Equipment) string { return e.ID })
	for start := 0; start < len(equipment); start += insertChunkSize {
		end := min(start+insertChunkSize, len(equipment))
		chunk := equipment[start:end]

		var sb strings.Builder
		sb.WriteString("INSERT INTO model_equipment (id, container_id) VALUES ")
		args := make([]any, 0, len(chunk)*2)
		for i, e := range chunk {
			if i > 0 {
				sb.WriteString(", ")
			}
			sb.WriteString("(")
			sb.WriteString(placeholders(2))
			sb.WriteString(")")
			args = append(args, e.ID, e.ContainerID)
		}
		sb.WriteString(" ON CONFLICT (id) DO UPDATE SET container_id = excluded.container_id")

		if _, err := tx.Exec(rebind(sb.String()), args...); err != nil {
			return fmt.Errorf("postgres: upserting equipment chunk (%d rows): %w", len(chunk), err)
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
	rows, err := m.db.Query(rebind(fmt.Sprintf(
		`SELECT id, type, parent_id FROM model_container WHERE id IN (%s)`,
		placeholders(len(ids)),
	)), idArgs(ids)...)
	if err != nil {
		return nil, fmt.Errorf("postgres: querying containers by id: %w", err)
	}
	defer rows.Close()
	return scanContainerRows(rows)
}

// GetChildren implements hierarchy.Store.
func (m *ModelStore) GetChildren(parentIDs []string) ([]coremodel.Container, error) {
	if len(parentIDs) == 0 {
		return nil, nil
	}
	rows, err := m.db.Query(rebind(fmt.Sprintf(
		`SELECT id, type, parent_id FROM model_container WHERE parent_id IN (%s)`,
		placeholders(len(parentIDs)),
	)), idArgs(parentIDs)...)
	if err != nil {
		return nil, fmt.Errorf("postgres: querying container children: %w", err)
	}
	defer rows.Close()
	return scanContainerRows(rows)
}

// GetDescendants implements hierarchy.Store. Uses a recursive CTE (DB-side
// fixpoint iteration, not Go-side stack recursion — see Idee.md's
// graph-traversal guidance) to walk the parent_id chain downward from
// rootIDs, any depth. PostgreSQL's WITH RECURSIVE syntax is identical to
// SQLite's here.
func (m *ModelStore) GetDescendants(rootIDs []string) ([]coremodel.Container, error) {
	if len(rootIDs) == 0 {
		return nil, nil
	}
	query := rebind(fmt.Sprintf(`
		WITH RECURSIVE descendants(id, type, parent_id) AS (
			SELECT id, type, parent_id FROM model_container WHERE parent_id IN (%s)
			UNION ALL
			SELECT c.id, c.type, c.parent_id
			FROM model_container c
			JOIN descendants d ON c.parent_id = d.id
		)
		SELECT id, type, parent_id FROM descendants
	`, placeholders(len(rootIDs))))

	rows, err := m.db.Query(query, idArgs(rootIDs)...)
	if err != nil {
		return nil, fmt.Errorf("postgres: querying container descendants: %w", err)
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
	return withTx(m.db, func(tx *sql.Tx) error {
		return upsertContainersTx(tx, containers)
	})
}

// upsertContainersTx is UpsertContainers' chunked-insert body — see
// upsertEquipmentTx's doc comment for why this is factored out.
func upsertContainersTx(tx *sql.Tx, containers []coremodel.Container) error {
	if len(containers) == 0 {
		return nil
	}
	containers = dedupeLast(containers, func(c coremodel.Container) string { return c.ID })
	for start := 0; start < len(containers); start += insertChunkSize {
		end := min(start+insertChunkSize, len(containers))
		chunk := containers[start:end]

		var sb strings.Builder
		sb.WriteString("INSERT INTO model_container (id, type, parent_id) VALUES ")
		args := make([]any, 0, len(chunk)*3)
		for i, c := range chunk {
			if i > 0 {
				sb.WriteString(", ")
			}
			sb.WriteString("(")
			sb.WriteString(placeholders(3))
			sb.WriteString(")")
			args = append(args, c.ID, string(c.Type), c.ParentID)
		}
		sb.WriteString(" ON CONFLICT (id) DO UPDATE SET type = excluded.type, parent_id = excluded.parent_id")

		if _, err := tx.Exec(rebind(sb.String()), args...); err != nil {
			return fmt.Errorf("postgres: upserting container chunk (%d rows): %w", len(chunk), err)
		}
	}
	return nil
}

// CountByType implements hierarchy.Store. Computed with a single GROUP BY
// query DB-side (see Usecases.md UC12).
func (m *ModelStore) CountByType() (map[string]int, error) {
	rows, err := m.db.Query(`SELECT type, COUNT(*) FROM model_container GROUP BY type`)
	if err != nil {
		return nil, fmt.Errorf("postgres: querying container counts by type: %w", err)
	}
	defer rows.Close()

	result := map[string]int{}
	for rows.Next() {
		var typ string
		var count int
		if err := rows.Scan(&typ, &count); err != nil {
			return nil, fmt.Errorf("postgres: scanning container count row: %w", err)
		}
		result[typ] = count
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres: iterating container count rows: %w", err)
	}
	return result, nil
}

func scanContainerRows(rows *sql.Rows) ([]coremodel.Container, error) {
	var result []coremodel.Container
	for rows.Next() {
		var c coremodel.Container
		var typ string
		if err := rows.Scan(&c.ID, &typ, &c.ParentID); err != nil {
			return nil, fmt.Errorf("postgres: scanning container row: %w", err)
		}
		c.Type = coremodel.ContainerType(typ)
		result = append(result, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres: iterating container rows: %w", err)
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
	rows, err := m.db.Query(rebind(fmt.Sprintf(
		`SELECT owner_id, owner_kind, lat, lon FROM model_geometry WHERE owner_id IN (%s)`,
		placeholders(len(ownerIDs)),
	)), idArgs(ownerIDs)...)
	if err != nil {
		return nil, fmt.Errorf("postgres: querying geometry by owner id: %w", err)
	}
	defer rows.Close()

	var result []coremodel.Geometry
	for rows.Next() {
		var g coremodel.Geometry
		var kind string
		if err := rows.Scan(&g.OwnerID, &kind, &g.Lat, &g.Lon); err != nil {
			return nil, fmt.Errorf("postgres: scanning geometry row: %w", err)
		}
		g.OwnerKind = coremodel.GeometryOwnerKind(kind)
		result = append(result, g)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres: iterating geometry rows: %w", err)
	}
	return result, nil
}

// InBoundingBox implements geometry.Store — a WGS84 range query (see
// Usecases.md UC3), computed DB-side via a plain indexless range scan
// (same rationale/caveat as internal/sqlite's identical method: revisit
// with a spatial index if/when real multi-GB models make this a
// bottleneck; PostgreSQL's PostGIS/box-range indexing would be the
// natural upgrade path here, deliberately not adopted yet).
func (m *ModelStore) InBoundingBox(minLat, minLon, maxLat, maxLon float64) ([]coremodel.Geometry, error) {
	rows, err := m.db.Query(
		rebind(`SELECT owner_id, owner_kind, lat, lon FROM model_geometry
		 WHERE lat BETWEEN ? AND ? AND lon BETWEEN ? AND ?`),
		minLat, maxLat, minLon, maxLon,
	)
	if err != nil {
		return nil, fmt.Errorf("postgres: querying geometry by bounding box: %w", err)
	}
	defer rows.Close()

	var result []coremodel.Geometry
	for rows.Next() {
		var g coremodel.Geometry
		var kind string
		if err := rows.Scan(&g.OwnerID, &kind, &g.Lat, &g.Lon); err != nil {
			return nil, fmt.Errorf("postgres: scanning geometry row: %w", err)
		}
		g.OwnerKind = coremodel.GeometryOwnerKind(kind)
		result = append(result, g)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres: iterating geometry rows: %w", err)
	}
	return result, nil
}

// UpsertGeometry implements geometry.Store.Upsert.
func (m *ModelStore) UpsertGeometry(geometries []coremodel.Geometry) error {
	if len(geometries) == 0 {
		return nil
	}
	cache, err := m.entityIDs()
	if err != nil {
		return err
	}
	return withTx(m.db, func(tx *sql.Tx) error {
		return upsertGeometryTx(tx, geometries, cache)
	})
}

// upsertGeometryTx is UpsertGeometry's chunked-insert body — see
// upsertEquipmentTx's doc comment for why this is factored out. cache
// resolves each Geometry's OwnerID to its entity_id.id (see entity_id.go).
func upsertGeometryTx(tx *sql.Tx, geometries []coremodel.Geometry, cache *entityIDCache) error {
	if len(geometries) == 0 {
		return nil
	}
	geometries = dedupeLast(geometries, func(g coremodel.Geometry) string { return g.OwnerID })

	ownerIDs := make([]string, len(geometries))
	for i, g := range geometries {
		ownerIDs[i] = g.OwnerID
	}
	for start := 0; start < len(geometries); start += insertChunkSize {
		end := min(start+insertChunkSize, len(geometries))
		chunk := geometries[start:end]

		ownerKeys, err := cache.resolveMany(tx, ownerIDs[start:end])
		if err != nil {
			return fmt.Errorf("postgres: resolving entity ids for geometry chunk: %w", err)
		}

		var sb strings.Builder
		sb.WriteString("INSERT INTO model_geometry_value (owner_id_key, owner_kind, lat, lon) VALUES ")
		args := make([]any, 0, len(chunk)*4)
		for i, g := range chunk {
			if i > 0 {
				sb.WriteString(", ")
			}
			sb.WriteString("(")
			sb.WriteString(placeholders(4))
			sb.WriteString(")")
			args = append(args, ownerKeys[g.OwnerID], string(g.OwnerKind), g.Lat, g.Lon)
		}
		sb.WriteString(` ON CONFLICT (owner_id_key) DO UPDATE SET
			owner_kind = excluded.owner_kind, lat = excluded.lat, lon = excluded.lon`)

		if _, err := tx.Exec(rebind(sb.String()), args...); err != nil {
			return fmt.Errorf("postgres: upserting geometry chunk (%d rows): %w", len(chunk), err)
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
	rows, err := m.db.Query(rebind(fmt.Sprintf(
		`SELECT id, kind FROM model_node WHERE id IN (%s)`,
		placeholders(len(ids)),
	)), idArgs(ids)...)
	if err != nil {
		return nil, fmt.Errorf("postgres: querying nodes by id: %w", err)
	}
	defer rows.Close()

	var result []coremodel.Node
	for rows.Next() {
		var n coremodel.Node
		var kind string
		if err := rows.Scan(&n.EquipmentID, &kind); err != nil {
			return nil, fmt.Errorf("postgres: scanning node row: %w", err)
		}
		n.Kind = coremodel.NodeKind(kind)
		result = append(result, n)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres: iterating node rows: %w", err)
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
	query := rebind(fmt.Sprintf(`
		SELECT DISTINCT e.equipment_id, e.terminal1_node_id, e.terminal2_node_id
		FROM model_edge_endpoint ep
		JOIN entity_id ent ON ent.id = ep.edge_id_key
		JOIN model_edge e ON e.equipment_id = ent.external_id
		WHERE ep.node_id_key IN (SELECT id FROM entity_id WHERE external_id IN (%s))
	`, placeholders(len(nodeIDs))))

	rows, err := m.db.Query(query, idArgs(nodeIDs)...)
	if err != nil {
		return nil, fmt.Errorf("postgres: querying edges by node id: %w", err)
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

// GetEdgesByEquipmentIDs implements topology/physical.Store — a direct
// primary-key lookup on model_edge.equipment_id, as opposed to
// GetEdgesByNodeIDs' node-centric bridge-table lookup.
func (m *ModelStore) GetEdgesByEquipmentIDs(equipmentIDs []string) ([]coremodel.Edge, error) {
	if len(equipmentIDs) == 0 {
		return nil, nil
	}
	rows, err := m.db.Query(rebind(fmt.Sprintf(
		`SELECT equipment_id, terminal1_node_id, terminal2_node_id FROM model_edge WHERE equipment_id IN (%s)`,
		placeholders(len(equipmentIDs)),
	)), idArgs(equipmentIDs)...)
	if err != nil {
		return nil, fmt.Errorf("postgres: querying edges by equipment id: %w", err)
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

// GetReachableNodes implements topology/physical.Store via a recursive CTE
// over model_edge_endpoint (DB-side fixpoint iteration, not Go-side
// recursion). PostgreSQL's WITH RECURSIVE syntax is identical to
// SQLite's here.
func (m *ModelStore) GetReachableNodes(rootNodeIDs []string) ([]string, error) {
	if len(rootNodeIDs) == 0 {
		return nil, nil
	}
	query := rebind(fmt.Sprintf(`
		WITH RECURSIVE reachable(node_key) AS (
			SELECT id FROM entity_id WHERE external_id IN (%s)
			UNION
			SELECT ep2.node_id_key
			FROM reachable r
			JOIN model_edge_endpoint ep1 ON ep1.node_id_key = r.node_key
			JOIN model_edge_endpoint ep2 ON ep2.edge_id_key = ep1.edge_id_key
		)
		SELECT ent.external_id FROM reachable r JOIN entity_id ent ON ent.id = r.node_key
	`, placeholders(len(rootNodeIDs))))

	rows, err := m.db.Query(query, idArgs(rootNodeIDs)...)
	if err != nil {
		return nil, fmt.Errorf("postgres: querying reachable nodes: %w", err)
	}
	defer rows.Close()

	var result []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("postgres: scanning reachable node row: %w", err)
		}
		result = append(result, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres: iterating reachable node rows: %w", err)
	}
	return result, nil
}

// UpsertNodes implements topology/physical.Store.
func (m *ModelStore) UpsertNodes(nodes []coremodel.Node) error {
	if len(nodes) == 0 {
		return nil
	}
	return withTx(m.db, func(tx *sql.Tx) error {
		return upsertNodesTx(tx, nodes)
	})
}

// upsertNodesTx is UpsertNodes' chunked-insert body — see
// upsertEquipmentTx's doc comment for why this is factored out.
func upsertNodesTx(tx *sql.Tx, nodes []coremodel.Node) error {
	if len(nodes) == 0 {
		return nil
	}
	nodes = dedupeLast(nodes, func(n coremodel.Node) string { return n.EquipmentID })
	for start := 0; start < len(nodes); start += insertChunkSize {
		end := min(start+insertChunkSize, len(nodes))
		chunk := nodes[start:end]

		var sb strings.Builder
		sb.WriteString("INSERT INTO model_node (id, kind) VALUES ")
		args := make([]any, 0, len(chunk)*2)
		for i, n := range chunk {
			if i > 0 {
				sb.WriteString(", ")
			}
			sb.WriteString("(")
			sb.WriteString(placeholders(2))
			sb.WriteString(")")
			args = append(args, n.EquipmentID, string(n.Kind))
		}
		sb.WriteString(" ON CONFLICT (id) DO UPDATE SET kind = excluded.kind")

		if _, err := tx.Exec(rebind(sb.String()), args...); err != nil {
			return fmt.Errorf("postgres: upserting node chunk (%d rows): %w", len(chunk), err)
		}
	}
	return nil
}

// UpsertEdges implements topology/physical.Store. Also maintains the
// model_edge_endpoint bridge table: any existing endpoint rows for an
// edge's EquipmentID are replaced (delete-then-insert) so re-upserting an
// Edge with different terminals never leaves stale endpoint rows behind.
// All three steps (edge upsert, endpoint delete, endpoint insert) are
// chunked multi-row statements instead of one Exec per edge/endpoint, to
// keep round-trip count bounded (see insertChunkSize's doc comment).
func (m *ModelStore) UpsertEdges(edges []coremodel.Edge) error {
	if len(edges) == 0 {
		return nil
	}
	cache, err := m.entityIDs()
	if err != nil {
		return err
	}
	return withTx(m.db, func(tx *sql.Tx) error {
		return upsertEdgesTx(tx, edges, cache)
	})
}

// upsertEdgesTx is UpsertEdges' 3-step chunked body — see
// upsertEquipmentTx's doc comment for why this is factored out. cache
// resolves each Edge's own ID and both terminal Node IDs to their
// entity_id.id (see entity_id.go).
func upsertEdgesTx(tx *sql.Tx, edges []coremodel.Edge, cache *entityIDCache) error {
	if len(edges) == 0 {
		return nil
	}
	edges = dedupeLast(edges, func(e coremodel.Edge) string { return e.EquipmentID })
	for start := 0; start < len(edges); start += insertChunkSize {
		end := min(start+insertChunkSize, len(edges))
		chunk := edges[start:end]

		// 1) Upsert the model_edge rows themselves.
		{
			var sb strings.Builder
			sb.WriteString("INSERT INTO model_edge (equipment_id, terminal1_node_id, terminal2_node_id) VALUES ")
			args := make([]any, 0, len(chunk)*3)
			for i, e := range chunk {
				if i > 0 {
					sb.WriteString(", ")
				}
				sb.WriteString("(")
				sb.WriteString(placeholders(3))
				sb.WriteString(")")
				args = append(args, e.EquipmentID, e.Terminal1NodeID, e.Terminal2NodeID)
			}
			sb.WriteString(` ON CONFLICT (equipment_id) DO UPDATE SET
				terminal1_node_id = excluded.terminal1_node_id,
				terminal2_node_id = excluded.terminal2_node_id`)
			if _, err := tx.Exec(rebind(sb.String()), args...); err != nil {
				return fmt.Errorf("postgres: upserting edge chunk (%d rows): %w", len(chunk), err)
			}
		}

		// Resolve every ID this chunk touches (edge IDs + both terminal
		// node IDs) to its entity_id.id up front, in one batched
		// resolveMany call, so steps 2/3 below work with plain int64 keys.
		var allIDs []string
		for _, e := range chunk {
			allIDs = append(allIDs, e.EquipmentID)
			if e.Terminal1NodeID != "" {
				allIDs = append(allIDs, e.Terminal1NodeID)
			}
			if e.Terminal2NodeID != "" {
				allIDs = append(allIDs, e.Terminal2NodeID)
			}
		}
		keys, err := cache.resolveMany(tx, allIDs)
		if err != nil {
			return fmt.Errorf("postgres: resolving entity ids for edge chunk: %w", err)
		}

		// 2) Clear any existing endpoint rows for this chunk's edges in
		// one statement (WHERE edge_id_key IN (...)) instead of one
		// DELETE per edge.
		{
			edgeKeys := make([]int64, len(chunk))
			for i, e := range chunk {
				edgeKeys[i] = keys[e.EquipmentID]
			}
			query := rebind(fmt.Sprintf(
				`DELETE FROM model_edge_endpoint WHERE edge_id_key IN (%s)`,
				placeholders(len(edgeKeys)),
			))
			args := make([]any, len(edgeKeys))
			for i, k := range edgeKeys {
				args[i] = k
			}
			if _, err := tx.Exec(query, args...); err != nil {
				return fmt.Errorf("postgres: clearing edge_endpoint rows for chunk: %w", err)
			}
		}

		// 3) Re-insert the (up to 2 per edge) endpoint rows in one
		// multi-row statement.
		{
			var sb strings.Builder
			sb.WriteString("INSERT INTO model_edge_endpoint (node_id_key, edge_id_key) VALUES ")
			args := make([]any, 0, len(chunk)*2)
			first := true
			for _, e := range chunk {
				edgeKey := keys[e.EquipmentID]
				for _, nodeID := range []string{e.Terminal1NodeID, e.Terminal2NodeID} {
					if nodeID == "" {
						continue
					}
					if !first {
						sb.WriteString(", ")
					}
					first = false
					sb.WriteString("(")
					sb.WriteString(placeholders(2))
					sb.WriteString(")")
					args = append(args, keys[nodeID], edgeKey)
				}
			}
			if !first { // at least one endpoint row to insert
				if _, err := tx.Exec(rebind(sb.String()), args...); err != nil {
					return fmt.Errorf("postgres: inserting edge_endpoint rows for chunk: %w", err)
				}
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
// across all of its groups (see internal/impl/usecase.ElectricallyConnected).
func (m *ModelStore) GetElectricalGroup(nodeIDs []string) (map[string][]string, error) {
	if len(nodeIDs) == 0 {
		return nil, nil
	}
	rows, err := m.db.Query(rebind(fmt.Sprintf(
		`SELECT node_id, group_id FROM model_electrical_group WHERE node_id IN (%s)`,
		placeholders(len(nodeIDs)),
	)), idArgs(nodeIDs)...)
	if err != nil {
		return nil, fmt.Errorf("postgres: querying electrical group by node id: %w", err)
	}
	defer rows.Close()

	result := map[string][]string{}
	for rows.Next() {
		var nodeID, groupID string
		if err := rows.Scan(&nodeID, &groupID); err != nil {
			return nil, fmt.Errorf("postgres: scanning electrical group row: %w", err)
		}
		result[nodeID] = append(result[nodeID], groupID)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres: iterating electrical group rows: %w", err)
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
	rows, err := m.db.Query(rebind(fmt.Sprintf(
		`SELECT DISTINCT node_id FROM model_electrical_group WHERE group_id IN (%s)`,
		placeholders(len(groupIDs)),
	)), idArgs(groupIDs)...)
	if err != nil {
		return nil, fmt.Errorf("postgres: querying electrical group members: %w", err)
	}
	defer rows.Close()

	var result []string
	for rows.Next() {
		var nodeID string
		if err := rows.Scan(&nodeID); err != nil {
			return nil, fmt.Errorf("postgres: scanning electrical group member row: %w", err)
		}
		result = append(result, nodeID)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres: iterating electrical group member rows: %w", err)
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
		return nil, fmt.Errorf("postgres: querying electrical group sizes: %w", err)
	}
	defer rows.Close()

	result := map[string]int{}
	for rows.Next() {
		var groupID string
		var count int
		if err := rows.Scan(&groupID, &count); err != nil {
			return nil, fmt.Errorf("postgres: scanning electrical group size row: %w", err)
		}
		result[groupID] = count
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres: iterating electrical group size rows: %w", err)
	}
	return result, nil
}

// UpsertElectricalGroups implements topology/electrical.Store.Upsert
// (renamed from plain Upsert to avoid colliding with the other Upsert*
// methods on this same ModelStore type). See
// internal/sqlite/model.go's identical method for the full rationale of
// the per-owner delete-then-insert strategy (concurrent Pass A station
// workers / Pass B never touch each other's rows).
func (m *ModelStore) UpsertElectricalGroups(owned map[string]map[string]string) error {
	if len(owned) == 0 {
		return nil
	}
	return withTx(m.db, func(tx *sql.Tx) error {
		return upsertElectricalGroupsTx(tx, owned)
	})
}

// upsertElectricalGroupsTx is UpsertElectricalGroups' chunked delete+insert
// body — see upsertEquipmentTx's doc comment for why this is factored out.
func upsertElectricalGroupsTx(tx *sql.Tx, owned map[string]map[string]string) error {
	if len(owned) == 0 {
		return nil
	}
	// Delete-by-owner in chunked IN(...) batches instead of one DELETE
	// per owner (owner count can be in the hundreds/thousands — one
	// per station batch/Pass B run).
	owners := make([]string, 0, len(owned))
	for owner := range owned {
		owners = append(owners, owner)
	}
	for start := 0; start < len(owners); start += insertChunkSize {
		end := min(start+insertChunkSize, len(owners))
		chunk := owners[start:end]
		query := rebind(fmt.Sprintf(
			`DELETE FROM model_electrical_group WHERE owner_id IN (%s)`,
			placeholders(len(chunk)),
		))
		if _, err := tx.Exec(query, idArgs(chunk)...); err != nil {
			return fmt.Errorf("postgres: deleting prior electrical groups for owner chunk: %w", err)
		}
	}

	// Flatten all (node_id, owner_id, group_id) rows across every
	// owner and insert them in chunked multi-row statements.
	type row struct{ nodeID, owner, groupID string }
	var rows []row
	for owner, groups := range owned {
		for nodeID, groupID := range groups {
			rows = append(rows, row{nodeID, owner, groupID})
		}
	}
	for start := 0; start < len(rows); start += insertChunkSize {
		end := min(start+insertChunkSize, len(rows))
		chunk := rows[start:end]

		var sb strings.Builder
		sb.WriteString("INSERT INTO model_electrical_group (node_id, owner_id, group_id) VALUES ")
		args := make([]any, 0, len(chunk)*3)
		for i, r := range chunk {
			if i > 0 {
				sb.WriteString(", ")
			}
			sb.WriteString("(")
			sb.WriteString(placeholders(3))
			sb.WriteString(")")
			args = append(args, r.nodeID, r.owner, r.groupID)
		}
		if _, err := tx.Exec(rebind(sb.String()), args...); err != nil {
			return fmt.Errorf("postgres: inserting electrical group chunk (%d rows): %w", len(chunk), err)
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
	rows, err := m.db.Query(rebind(fmt.Sprintf(
		`SELECT owner_id, key, value FROM model_attribute WHERE owner_id IN (%s) ORDER BY owner_id, key, seq`,
		placeholders(len(ownerIDs)),
	)), idArgs(ownerIDs)...)
	if err != nil {
		return nil, fmt.Errorf("postgres: querying attributes by owner id: %w", err)
	}
	defer rows.Close()

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

// UpsertAttributes implements technical.Store.Upsert (renamed from plain
// Upsert to avoid colliding with the other Upsert* methods on this same
// ModelStore type). See internal/sqlite/model.go's identical method for
// the full rationale of the per-(owner,key) delete-then-insert strategy.
// Both the delete and insert steps are chunked multi-row statements
// (row-value IN(...) for the delete, multi-row VALUES for the insert)
// instead of one Exec per (owner,key) pair / per attribute row — this is
// by far the highest-volume Upsert* call (hundreds of thousands of rows
// for a single lasttest import), so batching here matters the most.
func (m *ModelStore) UpsertAttributes(attributes []coremodel.Attribute) error {
	if len(attributes) == 0 {
		return nil
	}
	cache, err := m.attributeKeys()
	if err != nil {
		return err
	}
	entityCache, err := m.entityIDs()
	if err != nil {
		return err
	}
	return withTx(m.db, func(tx *sql.Tx) error {
		return upsertAttributesTx(tx, attributes, cache, entityCache)
	})
}

// upsertAttributesTx is UpsertAttributes' chunked delete+insert body —
// see upsertEquipmentTx's doc comment for why this is factored out. cache
// resolves each Attribute's Key to its attribute_key.id (creating a new
// attribute_key row on first-ever use of a name — see attribute_key.go),
// resolved once up front (Pass 0) so Pass 1/2 below work with plain
// int64 key IDs, same chunking strategy as before. entityCache likewise
// resolves each Attribute's OwnerID to its entity_id.id (see
// entity_id.go), also resolved up front in Pass 0.
func upsertAttributesTx(tx *sql.Tx, attributes []coremodel.Attribute, cache *attributeKeyCache, entityCache *entityIDCache) error {
	if len(attributes) == 0 {
		return nil
	}
	type ownerKey struct {
		owner string
		keyID int64
	}
	seq := map[ownerKey]int{}

	// Pass 0: resolve every distinct Key name to its attribute_key.id
	// once, avoiding redundant cache lookups for repeated keys within
	// this batch. Likewise resolve every distinct OwnerID to its
	// entity_id.id in one batched resolveMany call (see entity_id.go).
	keyIDs := make(map[coremodel.AttributeKey]int64, len(attributes))
	for _, a := range attributes {
		if _, ok := keyIDs[a.Key]; ok {
			continue
		}
		id, err := cache.resolve(tx, string(a.Key))
		if err != nil {
			return fmt.Errorf("postgres: resolving attribute key %q: %w", a.Key, err)
		}
		keyIDs[a.Key] = id
	}
	ownerIDs := make([]string, len(attributes))
	for i, a := range attributes {
		ownerIDs[i] = a.OwnerID
	}
	ownerKeys, err := entityCache.resolveMany(tx, ownerIDs)
	if err != nil {
		return fmt.Errorf("postgres: resolving entity ids for attribute owners: %w", err)
	}

	// Pass 1: determine the distinct (owner,key) pairs touched (in
	// first-seen order isn't important — a set suffices) and clear
	// their existing rows via chunked row-value IN(...) deletes.
	seen := map[ownerKey]bool{}
	var pairs []ownerKey
	for _, a := range attributes {
		ok := ownerKey{a.OwnerID, keyIDs[a.Key]}
		if !seen[ok] {
			seen[ok] = true
			pairs = append(pairs, ok)
		}
	}
	// Row-value IN uses 2 params/pair; keep the same insertChunkSize
	// row-count budget as everywhere else in this package.
	for start := 0; start < len(pairs); start += insertChunkSize {
		end := min(start+insertChunkSize, len(pairs))
		chunk := pairs[start:end]

		var sb strings.Builder
		sb.WriteString("DELETE FROM model_attribute_value WHERE (owner_id_key, key_id) IN (")
		args := make([]any, 0, len(chunk)*2)
		for i, p := range chunk {
			if i > 0 {
				sb.WriteString(", ")
			}
			sb.WriteString("(")
			sb.WriteString(placeholders(2))
			sb.WriteString(")")
			args = append(args, ownerKeys[p.owner], p.keyID)
		}
		sb.WriteString(")")
		if _, err := tx.Exec(rebind(sb.String()), args...); err != nil {
			return fmt.Errorf("postgres: clearing attribute rows for chunk: %w", err)
		}
	}

	// Pass 2: encode every value once, computing seq per (owner,key)
	// exactly as before, then insert in chunked multi-row statements.
	type encodedRow struct {
		owner string
		keyID int64
		seq   int
		value string
	}
	rows := make([]encodedRow, 0, len(attributes))
	for _, a := range attributes {
		ok := ownerKey{a.OwnerID, keyIDs[a.Key]}
		encoded, err := json.Marshal(a.Value)
		if err != nil {
			return fmt.Errorf("postgres: encoding attribute value for %s.%s: %w", a.OwnerID, a.Key, err)
		}
		rows = append(rows, encodedRow{a.OwnerID, keyIDs[a.Key], seq[ok], string(encoded)})
		seq[ok]++
	}

	for start := 0; start < len(rows); start += insertChunkSize {
		end := min(start+insertChunkSize, len(rows))
		chunk := rows[start:end]

		var sb strings.Builder
		sb.WriteString("INSERT INTO model_attribute_value (owner_id_key, key_id, seq, value) VALUES ")
		args := make([]any, 0, len(chunk)*4)
		for i, r := range chunk {
			if i > 0 {
				sb.WriteString(", ")
			}
			sb.WriteString("(")
			sb.WriteString(placeholders(4))
			sb.WriteString(")")
			args = append(args, ownerKeys[r.owner], r.keyID, r.seq, r.value)
		}
		if _, err := tx.Exec(rebind(sb.String()), args...); err != nil {
			return fmt.Errorf("postgres: inserting attribute chunk (%d rows): %w", len(chunk), err)
		}
	}
	return nil
}

// PersistBatch runs the writes for ONE Pass A/B batch — Containers,
// Equipment, Nodes, Edges, Attributes, Geometry, and electrical Groups —
// inside exactly ONE PostgreSQL transaction (one commit/fsync), instead of
// calling the individual Upsert* methods above one at a time (each of
// which opens/commits its own transaction). This matters specifically for
// PostgreSQL (unlike SQLite, which is in-process and pays no network/fsync
// cost per commit): a real lasttest-200-10-10 run measured ~400 station
// batches × 7 separate Upsert* transactions ≈ 2800 commits, each paying a
// full network round trip + fsync, which alone accounted for minutes of
// otherwise-unnecessary wall time. Collapsing this to one transaction per
// batch (≈400 commits total) is the fix. Any of Containers/Equipment/
// Nodes/Edges/Attributes/Geometries may be nil/empty; groups may be nil.
func (m *ModelStore) PersistBatch(
	containers []coremodel.Container,
	equipment []coremodel.Equipment,
	nodes []coremodel.Node,
	edges []coremodel.Edge,
	attributes []coremodel.Attribute,
	geometries []coremodel.Geometry,
	groups map[string]map[string]string,
) error {
	cache, err := m.attributeKeys()
	if err != nil {
		return err
	}
	entityCache, err := m.entityIDs()
	if err != nil {
		return err
	}
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
		if err := upsertEdgesTx(tx, edges, entityCache); err != nil {
			return err
		}
		if err := upsertAttributesTx(tx, attributes, cache, entityCache); err != nil {
			return err
		}
		if err := upsertGeometryTx(tx, geometries, entityCache); err != nil {
			return err
		}
		if err := upsertElectricalGroupsTx(tx, groups); err != nil {
			return err
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
		return fmt.Errorf("postgres: begin tx: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // no-op if committed

	if err := fn(tx); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("postgres: committing tx: %w", err)
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
// query — see internal/sqlite/model.go's identical helper for why a plain
// UNION ALL of single-column SELECTs is used instead of a
// "FROM (VALUES (?), (?)) AS roots(id)" table-alias form.
func unionAllSelects(n int) string {
	terms := make([]string, n)
	for i := range terms {
		terms[i] = "SELECT ?"
	}
	return strings.Join(terms, " UNION ALL ")
}
