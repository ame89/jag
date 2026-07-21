// circuits.go implements the NSC_SUPPORT feature's `circuit` / `circuit_network_device_map`
// tables: computing the real domain-model-connector's "NetIsland"/Circuit concept from an
// already-imported JAG model_* schema, reusing JAG's own existing, exported
// internal/impl/common.BuildCircuits ("Schaltkreis") implementation as a library call - this
// is the one part of jag2nsc that DOES import another JAG internal package, per explicit
// user instruction ("nur wenn nicht in jag ist, in jag2nsc ok" - i.e. calling jag's own
// exported logic from jag2nsc is fine; only ever MODIFYING a jag core file is forbidden).
// No jag core file is touched by this.
//
// Membership (which network_device belongs to which Circuit) comes straight out of
// BuildCircuits: PowerTransformer edges are galvanic boundaries (never merged), open
// switches interrupt, GND never participates - exactly the same rules verified against the
// real connector's CimToCondensedService.createNetIslandMapping/findConnectedEquipment
// (PowerTransformer-as-boundary, open-switch-interrupts).
//
// Naming (external_id/name) is the one part that ISN'T a pure graph computation in the real
// connector: external_id = "<PowerTransformerEnd mRID>-Island" (the raw CIM id of whichever
// PowerTransformerEnd the connector happened to process first for that island) and name =
// that End's own IdentifiedObject.name. Every dataset inspected has exactly one
// PowerTransformerEnd per PowerTransformer (jag's own model_edge always sets a
// PowerTransformer's Terminal2NodeID to GND - only one real Terminal/End is ever modeled),
// so a Circuit containing N transformers has exactly N PowerTransformerEnd candidates; this
// picks the lexicographically smallest End id (mRID) as the deterministic naming source -
// documented as a best-effort proxy for the real connector's genuinely iteration-order-
// dependent "first end processed" choice, not guaranteed byte-identical to a real import,
// but exactly matching whenever there is only one candidate (the common case: one
// transformer per circuit).
package jag2nsc

import (
	"context"
	"database/sql"
	_ "embed"
	"fmt"
	"sort"
	"strings"

	coremodel "gitlab.com/openk-nsc/jag/internal/core/model"
	"gitlab.com/openk-nsc/jag/internal/impl/common"
	"gitlab.com/openk-nsc/jag/internal/postgres"
)

//go:embed circuit_tables.sql
var circuitTablesSQL string

// ApplyCircuitTables (re-)creates the additive jag2nsc_circuit/jag2nsc_circuit_member
// tables (circuit_tables.sql). It does not populate them - call BuildCircuits afterwards.
func ApplyCircuitTables(ctx context.Context, db *sql.DB) error {
	if _, err := db.ExecContext(ctx, circuitTablesSQL); err != nil {
		return fmt.Errorf("jag2nsc: applying circuit_tables.sql: %w", err)
	}
	return nil
}

// transformerEnd is one CIM PowerTransformerEnd's own raw data, read directly from
// staging_records (see this file's doc comment - the same established loadRawTerminals/
// BuildNetworkGroup pattern; no jag core file is imported for this part).
type transformerEnd struct {
	id            string // the End's own mRID (rdf:about id)
	name          string // IdentifiedObject.name, "" if absent
	transformerID string // PowerTransformerEnd.PowerTransformer
}

// circuitRow is one output row destined for jag2nsc_circuit.
type circuitRow struct {
	key        string // jag's own Union-Find group id (join key to jag2nsc_circuit_member)
	externalID string
	name       string
}

// BuildCircuits (re-)populates jag2nsc_circuit/jag2nsc_circuit_member (full-replace) for the
// JAG database db is connected to. dsn must point at that SAME database (a second
// connection is opened internally to satisfy common.BuildCircuits' staging.Store
// parameter - see internal/postgres.Open).
func BuildCircuits(ctx context.Context, db *sql.DB, dsn string) error {
	nodes, err := loadAllNodes(ctx, db)
	if err != nil {
		return fmt.Errorf("jag2nsc: loading model_node: %w", err)
	}
	edges, err := loadAllEdges(ctx, db)
	if err != nil {
		return fmt.Errorf("jag2nsc: loading model_edge: %w", err)
	}

	version, err := latestStagingVersion(ctx, db)
	if err != nil {
		return fmt.Errorf("jag2nsc: determining staging version: %w", err)
	}
	if version == 0 {
		// No staging_records left (e.g. DeleteVersion was called) - nothing to compute.
		return clearCircuitTables(ctx, db)
	}

	store, err := postgres.Open(dsn)
	if err != nil {
		return fmt.Errorf("jag2nsc: opening staging store: %w", err)
	}
	defer store.Close()

	_, nodeCircuit, edgeCircuits, err := common.BuildCircuits(store, version, nodes, edges, nil)
	if err != nil {
		return fmt.Errorf("jag2nsc: common.BuildCircuits: %w", err)
	}

	transformerIDs, err := loadPowerTransformerIDs(ctx, db)
	if err != nil {
		return fmt.Errorf("jag2nsc: loading PowerTransformer ids: %w", err)
	}
	ends, err := loadTransformerEnds(ctx, db)
	if err != nil {
		return fmt.Errorf("jag2nsc: loading PowerTransformerEnd from staging_records: %w", err)
	}
	endsByTransformer := map[string][]transformerEnd{}
	for _, e := range ends {
		endsByTransformer[e.transformerID] = append(endsByTransformer[e.transformerID], e)
	}

	busbarNodes, err := loadBusbarContainerNodes(ctx, db)
	if err != nil {
		return fmt.Errorf("jag2nsc: loading busbar container nodes: %w", err)
	}

	// A BusbarSection or Junction can carry MULTIPLE raw CIM Terminals, each on its
	// OWN ConnectivityNode (one per bay/feeder wired to that bus, or per splice leg
	// of a multi-way cable joint) - jag's own 'busbar_node_id'/'junction_node_id'
	// Sachdaten attributes collapse all of them to a single canonical node id for
	// topology-walk purposes, which is right for jag2nsc's own walk (one logical
	// point) but throws away the fact that a DIFFERENT leg's raw ConnectivityNode
	// may be where some other equipment (e.g. a cable to a different busbar, or a
	// third transformer's feeder) actually attaches. common.BuildCircuits operates
	// on jag's model_edge/model_node - which likewise never bridges these raw
	// nodes - so equipment that is physically tied together only via a shared
	// BusbarSection or Junction can come out as separate Circuit groups (verified
	// against example_as_cim.xml: BusbarSections B-2-1/B-2-2/B-2-3 and Junction E-4
	// "Cable joint between B-2-1 and B-2-2" are what ties T-2-1/T-2-2/T-2-3
	// together into the real connector's single TEnd_291-Island). Fix: read every
	// BusbarSection/Junction's full raw ConnectivityNode set directly from
	// staging_records (mirrors the loadRawTerminals/BuildNetworkGroup exception -
	// no jag core file is touched) and union whichever Circuit groups those nodes
	// individually ended up in.
	bridgeNodes, err := loadBridgeNodeGroups(ctx, db)
	if err != nil {
		return fmt.Errorf("jag2nsc: loading busbar/junction raw ConnectivityNode groups: %w", err)
	}
	uf := newUnionFind()
	for _, rawNodes := range bridgeNodes {
		var first string
		for _, nid := range rawNodes {
			cid, ok := nodeCircuit[nid]
			if !ok {
				continue
			}
			if first == "" {
				first = cid
				uf.add(first)
				continue
			}
			uf.add(cid)
			uf.union(first, cid)
		}
	}
	resolve := func(cid string) string {
		if r, ok := uf.find(cid); ok {
			return r
		}
		return cid
	}

	houseCircuits, err := loadHouseCircuits(ctx, db, edges, nodeCircuit)
	if err != nil {
		return fmt.Errorf("jag2nsc: resolving house_connection circuits: %w", err)
	}

	// Group transformers by the single Circuit their (only) real Terminal touches - their
	// other Terminal is always GND (see this file's doc comment), so edgeCircuits[id] has
	// at most one entry for such an edge. Circuit groups bridged by a shared busbar (see
	// busbarRawNodes above) are merged via resolve().
	transformersByCircuit := map[string][]string{}
	for _, tID := range transformerIDs {
		cids := edgeCircuits[tID]
		if len(cids) == 0 {
			continue // isolated transformer, no other network device reachable - not part of any Circuit
		}
		cid := resolve(cids[0])
		transformersByCircuit[cid] = append(transformersByCircuit[cid], tID)
	}

	var rows []circuitRow
	rowIndex := map[string]bool{}
	members := map[string][]string{} // circuit_key -> device_id list

	addRow := func(cid, externalID, name string) {
		if rowIndex[cid] {
			return
		}
		rowIndex[cid] = true
		rows = append(rows, circuitRow{key: cid, externalID: externalID, name: name})
	}

	for cid, tIDs := range transformersByCircuit {
		var candidates []transformerEnd
		for _, tID := range tIDs {
			candidates = append(candidates, endsByTransformer[tID]...)
		}
		var externalID, name string
		if len(candidates) > 0 {
			sort.Slice(candidates, func(i, j int) bool { return candidates[i].id < candidates[j].id })
			chosen := candidates[0]
			externalID = chosen.id + "-Island"
			name = chosen.name
			if name == "" {
				name = chosen.id
			}
		} else {
			// No PowerTransformerEnd found at all for this group (shouldn't happen given
			// every PowerTransformer observed so far has exactly one) - fall back to the
			// transformer equipment id itself so the row is at least deterministic.
			sort.Strings(tIDs)
			externalID = tIDs[0] + "-Island"
			name = tIDs[0]
		}
		addRow(cid, externalID, name)
		members[cid] = append(members[cid], tIDs...)
	}

	// busbar containers: member of whatever Circuit their busbar_node_id lands in
	// (after resolving any busbar-bridged merge, see busbarRawNodes above).
	for containerID, nodeID := range busbarNodes {
		cid, ok := nodeCircuit[nodeID]
		if !ok {
			continue
		}
		cid = resolve(cid)
		// A Circuit with only busbars/houses (no transformer) still needs a row -
		// synthesize one keyed off the busbar container id, since there is no
		// PowerTransformerEnd to name it from.
		addRow(cid, containerID+"-Island", containerID)
		members[cid] = append(members[cid], containerID)
	}

	// house_connection containers: best-effort, see loadHouseCircuits's doc comment.
	for containerID, cid := range houseCircuits {
		cid = resolve(cid)
		addRow(cid, containerID+"-Island", containerID)
		members[cid] = append(members[cid], containerID)
	}

	return writeCircuits(ctx, db, rows, members)
}

func loadAllNodes(ctx context.Context, db *sql.DB) ([]coremodel.Node, error) {
	rows, err := db.QueryContext(ctx, `SELECT id, kind FROM model_node`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []coremodel.Node
	for rows.Next() {
		var n coremodel.Node
		var kind string
		if err := rows.Scan(&n.EquipmentID, &kind); err != nil {
			return nil, err
		}
		n.Kind = coremodel.NodeKind(kind)
		out = append(out, n)
	}
	return out, rows.Err()
}

func loadAllEdges(ctx context.Context, db *sql.DB) ([]coremodel.Edge, error) {
	rows, err := db.QueryContext(ctx, `SELECT equipment_id, terminal1_node_id, terminal2_node_id FROM model_edge`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []coremodel.Edge
	for rows.Next() {
		var e coremodel.Edge
		if err := rows.Scan(&e.EquipmentID, &e.Terminal1NodeID, &e.Terminal2NodeID); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func loadPowerTransformerIDs(ctx context.Context, db *sql.DB) ([]string, error) {
	rows, err := db.QueryContext(ctx, `
SELECT owner_id FROM model_attribute
WHERE key = 'cim_class' AND jag2nsc_attr_text(value) = 'PowerTransformer'`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// loadTransformerEnds reads every CIM PowerTransformerEnd object directly from
// staging_records - JAG's satellite folding (see views.sql's jag2nsc_transformer_satellite)
// discards each End's own id, keeping only its literal attribute values, so the End's own
// mRID (needed for external_id) is only recoverable from staging_records itself.
func loadTransformerEnds(ctx context.Context, db *sql.DB) ([]transformerEnd, error) {
	rows, err := db.QueryContext(ctx, `
SELECT id,
       MAX(value) FILTER (WHERE attribute = 'IdentifiedObject.name'),
       MAX(value) FILTER (WHERE attribute = 'PowerTransformerEnd.PowerTransformer')
FROM staging_records
WHERE class = 'PowerTransformerEnd'
  AND version = (SELECT MAX(version) FROM staging_records)
GROUP BY id
HAVING MAX(value) FILTER (WHERE attribute = 'PowerTransformerEnd.PowerTransformer') IS NOT NULL`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []transformerEnd
	for rows.Next() {
		var e transformerEnd
		var name sql.NullString
		if err := rows.Scan(&e.id, &name, &e.transformerID); err != nil {
			return nil, err
		}
		e.name = name.String
		out = append(out, e)
	}
	return out, rows.Err()
}

// loadBusbarContainerNodes maps every busbar container id to its ConnectivityNode id, via
// the 'busbar_node_id' attribute any equipment inside that container carries (see views.
// sql's existing use of the same attribute for the terminal/connection views).
func loadBusbarContainerNodes(ctx context.Context, db *sql.DB) (map[string]string, error) {
	rows, err := db.QueryContext(ctx, `
SELECT DISTINCT m.container_id, jag2nsc_attr_text(a.value)
FROM model_attribute a
         JOIN model_equipment m ON m.id = a.owner_id
WHERE a.key = 'busbar_node_id'`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var containerID, nodeID string
		if err := rows.Scan(&containerID, &nodeID); err != nil {
			return nil, err
		}
		out[containerID] = nodeID
	}
	return out, rows.Err()
}

// loadBridgeNodeGroups maps every BusbarSection container id and every Junction equipment
// id to the FULL, unmerged set of raw CIM ConnectivityNode ids any of its Terminals sits on
// - read directly from staging_records (Terminal.ConductingEquipment/Terminal.
// ConnectivityNode), since jag's own 'busbar_node_id'/'junction_node_id' Sachdaten
// attributes already collapse these to one canonical value per equipment (see this file's
// BuildCircuits doc comment for why that loses cross-equipment bridging info). BusbarSection
// Terminals are spread across jag's per-terminal equipment variants (e.g. "B-2-2", "B-2-2#2",
// ...), grouped here by their shared busbar container id; a Junction's own Terminals all
// share its single raw equipment id directly.
func loadBridgeNodeGroups(ctx context.Context, db *sql.DB) (map[string][]string, error) {
	rows, err := db.QueryContext(ctx, `
SELECT m.container_id AS group_id, sr2.value AS node_id
FROM model_equipment m
JOIN staging_records sr ON sr.attribute = 'Terminal.ConductingEquipment'
                        AND sr.value = m.id
                        AND sr.version = (SELECT MAX(version) FROM staging_records)
JOIN staging_records sr2 ON sr2.id = sr.id
                         AND sr2.attribute = 'Terminal.ConnectivityNode'
                         AND sr2.version = sr.version
WHERE m.container_id LIKE 'busbar:%'

UNION ALL

SELECT a.owner_id AS group_id, sr2.value AS node_id
FROM model_attribute a
JOIN staging_records sr ON sr.attribute = 'Terminal.ConductingEquipment'
                        AND sr.value = a.owner_id
                        AND sr.version = (SELECT MAX(version) FROM staging_records)
JOIN staging_records sr2 ON sr2.id = sr.id
                         AND sr2.attribute = 'Terminal.ConnectivityNode'
                         AND sr2.version = sr.version
WHERE a.key = 'cim_class' AND jag2nsc_attr_text(a.value) = 'Junction'`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string][]string{}
	seen := map[string]map[string]bool{}
	for rows.Next() {
		var groupID, nodeID string
		if err := rows.Scan(&groupID, &nodeID); err != nil {
			return nil, err
		}
		if seen[groupID] == nil {
			seen[groupID] = map[string]bool{}
		}
		if seen[groupID][nodeID] {
			continue
		}
		seen[groupID][nodeID] = true
		out[groupID] = append(out[groupID], nodeID)
	}
	return out, rows.Err()
}

// unionFind is a minimal disjoint-set over string keys, used to merge Circuit group ids
// that a shared busbar's raw ConnectivityNodes reveal are actually one physical Circuit.
type unionFind struct {
	parent map[string]string
}

func newUnionFind() *unionFind {
	return &unionFind{parent: map[string]string{}}
}

func (u *unionFind) add(key string) {
	if _, ok := u.parent[key]; !ok {
		u.parent[key] = key
	}
}

func (u *unionFind) find(key string) (string, bool) {
	root, ok := u.parent[key]
	if !ok {
		return "", false
	}
	for root != u.parent[root] {
		root = u.parent[root]
	}
	// path compression
	for key != root {
		next := u.parent[key]
		u.parent[key] = root
		key = next
	}
	return root, true
}

func (u *unionFind) union(a, b string) {
	u.add(a)
	u.add(b)
	ra, _ := u.find(a)
	rb, _ := u.find(b)
	if ra != rb {
		u.parent[ra] = rb
	}
}

// loadHouseCircuits is a best-effort resolution of which Circuit each `house` container
// belongs to: a house_connection has no Node of its own (unlike a busbar) - its only
// physical presence is the Edge(s) of the equipment living inside it (a feeder Fuse/Switch/
// PowerElectronicsConnection/...). This takes the union of nodeCircuit[nid] for every
// non-GND Terminal node touched by any equipment inside the house container, picking the
// lexicographically smallest resulting Circuit id if more than one is touched (rare, would
// mean the house's own feeder equipment already spans two Circuits, e.g. an open switch
// right at the house boundary) - documented as an approximation, not a guaranteed exact
// match to the real connector's own equivalent traversal.
func loadHouseCircuits(ctx context.Context, db *sql.DB, edges []coremodel.Edge, nodeCircuit map[string]string) (map[string]string, error) {
	houseIDs, err := queryStrings(ctx, db, `SELECT id FROM model_container WHERE type = 'house'`)
	if err != nil {
		return nil, err
	}
	if len(houseIDs) == 0 {
		return map[string]string{}, nil
	}

	equipmentContainer, err := loadEquipmentContainers(ctx, db, houseIDs)
	if err != nil {
		return nil, err
	}

	out := map[string]string{}
	for _, e := range edges {
		houseID, ok := equipmentContainer[e.EquipmentID]
		if !ok {
			continue
		}
		for _, nid := range []string{e.Terminal1NodeID, e.Terminal2NodeID} {
			if nid == common.GNDNodeID {
				continue
			}
			cid, ok := nodeCircuit[nid]
			if !ok {
				continue
			}
			if existing, has := out[houseID]; !has || cid < existing {
				out[houseID] = cid
			}
		}
	}
	return out, nil
}

// loadEquipmentContainers maps every equipment id whose container_id is one of
// containerIDs to that container id (used to find a house's own feeder equipment).
func loadEquipmentContainers(ctx context.Context, db *sql.DB, containerIDs []string) (map[string]string, error) {
	placeholders := make([]string, len(containerIDs))
	args := make([]any, len(containerIDs))
	for i, id := range containerIDs {
		placeholders[i] = fmt.Sprintf("$%d", i+1)
		args[i] = id
	}
	query := fmt.Sprintf(`SELECT id, container_id FROM model_equipment WHERE container_id IN (%s)`, strings.Join(placeholders, ","))
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var id, containerID string
		if err := rows.Scan(&id, &containerID); err != nil {
			return nil, err
		}
		out[id] = containerID
	}
	return out, rows.Err()
}

func queryStrings(ctx context.Context, db *sql.DB, query string, args ...any) ([]string, error) {
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func latestStagingVersion(ctx context.Context, db *sql.DB) (uint64, error) {
	var v sql.NullInt64
	if err := db.QueryRowContext(ctx, `SELECT MAX(version) FROM staging_records`).Scan(&v); err != nil {
		return 0, err
	}
	if !v.Valid {
		return 0, nil
	}
	return uint64(v.Int64), nil
}

func clearCircuitTables(ctx context.Context, db *sql.DB) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `TRUNCATE jag2nsc_circuit_member`); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `TRUNCATE jag2nsc_circuit CASCADE`); err != nil {
		return err
	}
	return tx.Commit()
}

func writeCircuits(ctx context.Context, db *sql.DB, rows []circuitRow, members map[string][]string) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `TRUNCATE jag2nsc_circuit_member`); err != nil {
		return fmt.Errorf("truncating jag2nsc_circuit_member: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `TRUNCATE jag2nsc_circuit CASCADE`); err != nil {
		return fmt.Errorf("truncating jag2nsc_circuit: %w", err)
	}

	for _, r := range rows {
		if _, err := tx.ExecContext(ctx, `INSERT INTO jag2nsc_circuit (circuit_key, external_id, name) VALUES ($1, $2, $3)`, r.key, r.externalID, r.name); err != nil {
			return fmt.Errorf("inserting jag2nsc_circuit %q: %w", r.key, err)
		}
	}
	for cid, deviceIDs := range members {
		seen := map[string]bool{}
		for _, id := range deviceIDs {
			if seen[id] {
				continue
			}
			seen[id] = true
			if _, err := tx.ExecContext(ctx, `INSERT INTO jag2nsc_circuit_member (circuit_key, device_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`, cid, id); err != nil {
				return fmt.Errorf("inserting jag2nsc_circuit_member (%q, %q): %w", cid, id, err)
			}
		}
	}

	return tx.Commit()
}
