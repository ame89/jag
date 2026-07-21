// topology.go implements the NSC_SUPPORT feature: an additive, opt-in Go re-implementation
// of the real domain-model-connector's chain-collapsing walk (ConnectionBuilderService/
// CondensedCimMapper), operating purely as a POST-PROCESSING step on top of an already
// fully-imported JAG "model_*" schema in Postgres.
//
// This file, and everything it calls, is 100% additive: it only ever reads model_equipment/
// model_edge/model_container/model_attribute (already populated by JAG's own, completely
// unmodified, import pipeline - phase1/RunPassA/RunPassB) and writes to the new
// jag2nsc_topo_terminal/jag2nsc_topo_connection/jag2nsc_topo_connection_terminal_map/jag2nsc_topo_line_segment
// tables (topology_tables.sql). No existing JAG Go source file (internal/impl/common,
// internal/postgres, cmd/phase2check, ...) is imported, called, or modified by this code.
//
// Algorithm (ported from the real connector's ConnectionBuilderService.getConnections/
// extendConnections/getLineSegmentsSwitchesAndEndTerminals, verified against its source):
//
//  1. Start one walk from every BusbarSection equipment's own (single, busbar_node_id-
//     derived) terminal.
//  2. At every step, look at all OTHER equipment terminals sharing the current node
//     (excluding the one we just arrived via) - one recursive branch per such neighbor.
//  3. A branch STOPS and completes a `connection` as soon as it reaches a BusbarSection,
//     PowerTransformer, or an equipment living directly inside a `house` container, or hits
//     a genuine dead end (no further equipment at all beyond the current node).
//  4. A branch KEEPS WALKING through Fuse/Switch/ACLineSegment/other two-terminal equipment
//     that isn't itself a stop condition, extending through the equipment's OTHER terminal.
//  5. Every ACLineSegment encountered along a walk contributes one `line_segment` row
//     (ordered, sequence_number 1..N) to that walk's eventual `connection`.
//  6. Every branch/junction point (a node where the walk has >1 further neighbor, i.e. a
//     genuine T-tap) as well as every non-Fuse Switch mid-chain also gets its own `terminal`
//     row (feeding `connection_terminal_map`), in addition to the two walk endpoints.
//
// Known deliberate simplifications vs. the exact real algorithm (see README.md "Known
// limitations"): a busbar-to-busbar walk that would be discovered a second time from the
// opposite direction is only counted once (matching the real connector's isAlreadyProcessed
// bookkeeping). These are documented gaps, not silent guesses.
package jag2nsc

import (
	"context"
	"database/sql"
	_ "embed"
	"fmt"
	"io"
	"log/slog"
	"sort"
	"strings"

	"gitlab.com/openk-nsc/jag/internal/progress"
)

// progressLogger is used by BuildTopology to report per-phase progress (record counts,
// elapsed time, RAM) while it runs - see internal/progress's doc comment. Defaults to
// discarding everything so existing callers/tests see no behavior change; call SetLogger to
// opt in (cmd/jag2nsc-apply does, mirroring internal/impl/common's own SetLogger/logger
// package-level knob). This only READS internal/progress (an existing, unmodified JAG
// helper package) - no jag core file is changed by this.
var progressLogger = slog.New(slog.NewTextHandler(io.Discard, nil))

// SetLogger installs l as the progress logger for this package's BuildTopology phases.
// Passing nil is a no-op (keeps the previous logger).
func SetLogger(l *slog.Logger) {
	if l == nil {
		return
	}
	progressLogger = l
}

func newProgress(phase string) *progress.Reporter {
	return progress.New(progressLogger, phase)
}

//go:embed topology_tables.sql
var topologyTablesSQL string

// equipmentInfo is the static, per-equipment metadata needed by the walk: its CIM class and
// which container it lives in (to detect the "lives inside a house container" stop
// condition and the "lives inside a bay container" feeder-area assignment).
type equipmentInfo struct {
	class       string
	containerID string
}

// stub is one equipment's single real CIM Terminal instance sitting on a given graph node.
type stub struct {
	equipmentID string
	terminalKey string // the real CIM Terminal id (see loadGraph)
	node        string // the ConnectivityNode this specific Terminal sits on
	// otherNodes are the node(s) on the opposite side of this equipment reachable from here:
	// exactly one for ordinary two-terminal equipment (Fuse/Switch/ACLineSegment/a 2-node
	// Junction splice), 2+ for a multi-way splice (3+ own Terminals - a branching Junction,
	// or a BusbarSection with more than 2 real Terminals - arriving via any one of them fans
	// out to every OTHER one, exactly like a T-tap), and empty for a single-Terminal
	// equipment (nothing to walk further from there itself - it is only ever a walk
	// endpoint, never something walked "through").
	otherNodes []string
}


// graph is the whole-database physical graph loaded once per BuildTopology call.
type graph struct {
	equipment    map[string]equipmentInfo    // equipment_id -> class/container
	container    map[string]string           // container_id -> type
	nodeStubs    map[string][]stub           // node_id -> every terminal instance sitting on it
	ownTerminals map[string][]stub           // equipment_id -> every one of its OWN terminal instances (used to start a walk from every one of a BusbarSection's real Terminals, not just a single hardcoded one)
}

// terminalRow/connectionRow/lineSegmentRow mirror topology_tables.sql's shape exactly - kept
// as plain structs so the walk can build them in memory before a single, full-replace write.
type terminalRow struct {
	terminalKey        string
	equipmentID        string
	cimClass           string
	networkDeviceExtID string
	deviceKind         string
	feederAreaExtID    string
	terminalType       string
}

type connectionRow struct {
	connectionKey     string
	externalID        string
	sourceTerminalKey string
	targetTerminalKey string
	sourceDeviceKind  string
	targetDeviceKind  string
	// midTerminalKeys are the terminal instances of Switch/Junction equipment encountered
	// strictly BETWEEN source and target (in walk order) - mirrors the real connector's
	// createTerminalsAndFeederEnds loop, which records an own Terminal for every mid-chain
	// Switch or Junction (but explicitly NOT Fuse, see topology.go's step()). Empty for the
	// common case of a connection with no such mid-chain equipment.
	midTerminalKeys []string
}

type lineSegmentRow struct {
	lineKey           string
	connectionKey     string
	aclineEquipmentID string
	sequenceNumber    int
}

// topologyResult accumulates every row produced across all walks, deduplicated by key
// (several branches/walks can legitimately re-discover the same junction terminal - see
// package doc comment - so terminals are a map, not a slice).
type topologyResult struct {
	terminals   map[string]terminalRow
	connections []connectionRow
	lineSegs    []lineSegmentRow
}

// stopKind classifies why a walk branch terminated.
type stopKind int

const (
	stopNone      stopKind = iota // not a stop - keep walking
	stopBusbar                    // reached a BusbarSection
	stopTransformer                // reached a PowerTransformer
	stopHouse                     // reached equipment living directly inside a `house` container
	stopDeadEnd                   // no further equipment beyond this node at all
)

// classify decides, for equipment eqID sitting at the far end of a step, whether the walk
// must stop here (and how), mirroring ConnectionBuilderService.getConnections' explicit
// termination checks (BusbarSection / PowerTransformer / Building-equivalent).
func (g *graph) classify(eqID string) stopKind {
	info := g.equipment[eqID]
	switch info.class {
	case "BusbarSection":
		return stopBusbar
	case "PowerTransformer":
		return stopTransformer
	}
	if g.container[info.containerID] == "house" {
		return stopHouse
	}
	return stopNone
}

// deviceKindOf maps a stop's anchor equipment to domain_model's device_type enum, and
// resolves the "network device" external id that terminal.network_device_id/
// connection.source_device_type ultimately need: the BUSBAR container id (busbar and
// house_connection are container-identified in domain_model, not equipment-identified),
// the PowerTransformer equipment id itself, or the house container id.
func (g *graph) deviceKindAndID(kind stopKind, eqID string) (deviceKind, externalID string) {
	switch kind {
	case stopBusbar:
		return "BUSBAR", g.equipment[eqID].containerID
	case stopTransformer:
		return "TRANSFORMER", eqID
	case stopHouse:
		return "HOUSE_CONNECTION", g.equipment[eqID].containerID
	default:
		return "", ""
	}
}

// walker carries the small amount of mutable, per-call state the recursive walk needs.
type walker struct {
	g                *graph
	result           *topologyResult
	completedBusbars map[string]bool // busbars whose OWN outgoing walks have already fully run - used to dedupe a bidirectional busbar<->busbar link exactly like the real connector's isAlreadyProcessed guard
	connSeq          int
}

// branchState is one in-flight walk branch: which equipment we last passed through, which
// node we arrived at (via that equipment's OTHER terminal), the ordered ACLineSegments
// collected so far, and the terminal-key of the walk's very first (start-anchor-adjacent)
// equipment.
type branchState struct {
	startTerminalKey string
	lastEquipmentID  string
	atNode           string
	lineSegments     []string // ACLineSegment equipment ids, in walk order
	// midTerminalKeys collects the terminal-keys of every mid-chain Switch/Junction
	// encountered so far, in walk order - see connectionRow.midTerminalKeys.
	midTerminalKeys []string
}

// BuildTopology (re-)computes the entire chain-collapsed domain_model-shaped topology from
// the already-imported model_* tables and (re-)writes jag2nsc_topo_terminal/jag2nsc_topo_connection/
// jag2nsc_topo_connection_terminal_map/jag2nsc_topo_line_segment in one full-replace transaction. It
// is the only entry point of the NSC_SUPPORT feature; callers must have already applied
// topology_tables.sql (see ApplyTopologyTables) and views.sql.
func BuildTopology(ctx context.Context, db *sql.DB) error {
	rp := newProgress("jag2nsc-raw-terminals")
	n, err := loadRawTerminals(ctx, db)
	if err != nil {
		return fmt.Errorf("jag2nsc: loading raw terminals from staging_records: %w", err)
	}
	rp.Tick(n)
	rp.Done()

	wp := newProgress("jag2nsc-topology-walk")
	g, err := loadGraph(ctx, db)
	if err != nil {
		return fmt.Errorf("jag2nsc: loading graph for topology: %w", err)
	}

	w := &walker{g: g, result: &topologyResult{terminals: map[string]terminalRow{}}, completedBusbars: map[string]bool{}}
	w.run()
	wp.Tick(len(w.result.terminals) + len(w.result.connections) + len(w.result.lineSegs))

	if err := writeTopology(ctx, db, w.result); err != nil {
		return fmt.Errorf("jag2nsc: writing topology: %w", err)
	}
	wp.Done()
	return nil
}

// loadRawTerminals (re-)populates jag2nsc_raw_terminal (full-replace) directly from JAG's
// own staging_records - the raw CIM Terminal objects (ConductingEquipment/ConnectivityNode/
// sequenceNumber), pivoted from staging_records' entity-attribute-value shape into one row
// per real Terminal id. This was originally the ONLY place jag2nsc read staging_records;
// network_group.go's loadNetworkGroups (SubGeographicalRegion) and circuits.go's
// loadTransformerEnds (PowerTransformerEnd) now use the same established pattern for their
// own CIM classes - everything else in this package keeps reading model_*/jag2nsc_* tables
// as before. Uses the latest
// staging version present (mirrors JAG's own "most recent import wins" semantics - see
// staging_version_counter). Returns the number of rows written, for progress reporting.
func loadRawTerminals(ctx context.Context, db *sql.DB) (int, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `TRUNCATE jag2nsc_raw_terminal`); err != nil {
		return 0, fmt.Errorf("truncating jag2nsc_raw_terminal: %w", err)
	}

	// staging_records may hold rows for more than one still-present version (an old one
	// that was never cleaned up plus the current one) - always use the newest, exactly like
	// JAG's own staging_version_counter.last_version tracks it.
	res, err := tx.ExecContext(ctx, `
INSERT INTO jag2nsc_raw_terminal (terminal_id, equipment_id, node_id, sequence_number)
SELECT id,
       MAX(value) FILTER (WHERE attribute = 'Terminal.ConductingEquipment') AS equipment_id,
       MAX(value) FILTER (WHERE attribute = 'Terminal.ConnectivityNode') AS node_id,
       NULLIF(MAX(value) FILTER (WHERE attribute = 'ACDCTerminal.sequenceNumber'), '')::integer AS seq
FROM staging_records
WHERE class = 'Terminal'
  AND version = (SELECT MAX(version) FROM staging_records)
GROUP BY id
HAVING MAX(value) FILTER (WHERE attribute = 'Terminal.ConductingEquipment') IS NOT NULL
   AND MAX(value) FILTER (WHERE attribute = 'Terminal.ConnectivityNode') IS NOT NULL`)
	if err != nil {
		return 0, fmt.Errorf("populating jag2nsc_raw_terminal from staging_records: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}


// ApplyTopologyTables (re-)creates the additive jag2nsc_topo_terminal/jag2nsc_topo_connection/
// jag2nsc_topo_connection_terminal_map/jag2nsc_topo_line_segment tables (topology_tables.sql). It
// does not populate them - call BuildTopology afterwards for that.
func ApplyTopologyTables(ctx context.Context, db *sql.DB) error {
	if _, err := db.ExecContext(ctx, topologyTablesSQL); err != nil {
		return fmt.Errorf("jag2nsc: applying topology_tables.sql: %w", err)
	}
	return nil
}

// loadGraph reads every piece of static metadata the walk needs: equipment class +
// container (from model_equipment/model_container/model_attribute - unaffected by any of
// JAG's own node-merging, so still reliable as-is), plus the REAL per-Terminal physical
// graph from jag2nsc_raw_terminal (populated by loadRawTerminals directly from JAG's
// staging_records - see that function's doc comment for why this replaces model_edge/
// busbar_node_id/junction_node_id as the connectivity source).
//
// Every equipment's own terminal set (grouped by equipment_id, ordered by
// sequence_number/terminal_id for determinism) is turned into stubs by terminal COUNT
// alone, uniformly across every CIM class - exactly mirroring the real connector's own
// generic Terminal-based walk (it never special-cases BusbarSection/Junction/Fuse/Switch
// for CONNECTIVITY, only for the stop/mid-chain-terminal decisions in classify()/step()):
//   - 1 terminal: a bare anchor stub (otherNodes empty) - BusbarSection, PowerTransformer,
//     Meter, or any other single-terminal source/sink.
//   - 2 terminals: an ordinary two-terminal Zweipol (Fuse/Switch/ACLineSegment/a plain
//     2-node Junction splice/...) - each terminal's stub points at the other's node.
//   - 3+ terminals: a genuine multi-way splice (a branching Junction, or a BusbarSection
//     with more than 2 real CIM Terminals) - each terminal's stub fans out to every OTHER
//     terminal's node, exactly like a T-tap.
//
// terminalKey is always the REAL CIM Terminal id (e.g. "B-1-1-E-1"), not a synthetic
// "equipmentID#T1" - this both removes the need for any of the old special-casing AND makes
// jag2nsc's own terminal.external_id naming match the real domain-model-connector's 1:1.
// baseEquipmentID reverses JAG's own import-time BusbarSection Terminal ID
// splitting (internal/importer/nsc/normalize.go: "<busbarID>#N" synthetic
// copies) by stripping a trailing "#<digits>" suffix, if present, so all of a
// BusbarSection's raw Terminals group back under its one real equipment ID.
func baseEquipmentID(id string) string {
	if i := strings.LastIndexByte(id, '#'); i >= 0 {
		suffix := id[i+1:]
		if suffix != "" {
			allDigits := true
			for _, r := range suffix {
				if r < '0' || r > '9' {
					allDigits = false
					break
				}
			}
			if allDigits {
				return id[:i]
			}
		}
	}
	return id
}

func loadGraph(ctx context.Context, db *sql.DB) (*graph, error) {
	g := &graph{
		equipment:    map[string]equipmentInfo{},
		container:    map[string]string{},
		nodeStubs:    map[string][]stub{},
		ownTerminals: map[string][]stub{},
	}

	containerRows, err := db.QueryContext(ctx, `SELECT id, type FROM model_container`)
	if err != nil {
		return nil, fmt.Errorf("querying model_container: %w", err)
	}
	for containerRows.Next() {
		var id, typ string
		if err := containerRows.Scan(&id, &typ); err != nil {
			containerRows.Close()
			return nil, err
		}
		g.container[id] = typ
	}
	if err := containerRows.Err(); err != nil {
		return nil, err
	}
	containerRows.Close()

	eqRows, err := db.QueryContext(ctx, `
SELECT eq.id, eq.container_id, COALESCE(cls.value, '')
FROM model_equipment eq
         LEFT JOIN (
    SELECT owner_id, value FROM model_attribute WHERE key = 'cim_class' AND seq = 0
    ) cls ON cls.owner_id = eq.id`)
	if err != nil {
		return nil, fmt.Errorf("querying model_equipment: %w", err)
	}
	for eqRows.Next() {
		var id, containerID, class string
		if err := eqRows.Scan(&id, &containerID, &class); err != nil {
			eqRows.Close()
			return nil, err
		}
		g.equipment[id] = equipmentInfo{class: unwrapAttrText(class), containerID: containerID}
	}
	if err := eqRows.Err(); err != nil {
		return nil, err
	}
	eqRows.Close()

	// Build every equipment's own ordered Terminal list directly from jag2nsc_raw_terminal
	// (the RAW, pre-merge CIM Terminal objects - see loadRawTerminals). Ordered by
	// sequence_number first (ACDCTerminal.sequenceNumber, NULLS LAST since it's optional in
	// the source data), then terminal_id as a stable tiebreaker - this fixes stub fan-out
	// order deterministically across repeated runs.
	rawRows, err := db.QueryContext(ctx, `
SELECT terminal_id, equipment_id, node_id
FROM jag2nsc_raw_terminal
ORDER BY equipment_id, sequence_number NULLS LAST, terminal_id`)
	if err != nil {
		return nil, fmt.Errorf("querying jag2nsc_raw_terminal: %w", err)
	}
	type rawTerm struct{ terminalID, equipmentID, nodeID string }
	byEquipment := map[string][]rawTerm{}
	var order []string // equipment_id, first-seen order, for deterministic map iteration below
	for rawRows.Next() {
		var t rawTerm
		if err := rawRows.Scan(&t.terminalID, &t.equipmentID, &t.nodeID); err != nil {
			rawRows.Close()
			return nil, err
		}
		// JAG's own import normalization (internal/importer/nsc/normalize.go,
		// StreamFile) rewrites Terminal.ConductingEquipment for every
		// BusbarSection Terminal beyond the first to a synthetic "<busbarID>#N"
		// value (and creates a matching synthetic model_equipment row), so that
		// its own model_edge/model_node graph gets one node-role graph vertex
		// per raw Terminal without colliding IDs. jag2nsc must undo this here:
		// all of a BusbarSection's raw Terminals belong to the SAME real
		// network_device ("B-2-2"), not to N distinct equipments - so strip the
		// synthetic "#N" suffix before grouping, exactly reversing that rewrite.
		t.equipmentID = baseEquipmentID(t.equipmentID)
		if _, ok := byEquipment[t.equipmentID]; !ok {
			order = append(order, t.equipmentID)
		}
		byEquipment[t.equipmentID] = append(byEquipment[t.equipmentID], t)
	}
	if err := rawRows.Err(); err != nil {
		return nil, err
	}
	rawRows.Close()

	// Turn each equipment's own Terminal list into stubs purely by COUNT - see this
	// function's doc comment for the full rationale (1/2/3+ cases).
	for _, eqID := range order {
		terms := byEquipment[eqID]
		nodes := make([]string, len(terms))
		for i, t := range terms {
			nodes[i] = t.nodeID
		}
		for i, t := range terms {
			var others []string
			if len(terms) >= 2 {
				others = make([]string, 0, len(terms)-1)
				for j, n := range nodes {
					if j != i {
						others = append(others, n)
					}
				}
			}
			s := stub{equipmentID: eqID, terminalKey: t.terminalID, node: t.nodeID, otherNodes: others}
			g.nodeStubs[t.nodeID] = append(g.nodeStubs[t.nodeID], s)
			g.ownTerminals[eqID] = append(g.ownTerminals[eqID], s)
		}
	}

	return g, nil
}

// unwrapAttrText mirrors views.sql's jag2nsc_attr_text(): JAG's model_attribute.value is
// occasionally a small JSON-quoted string rather than a plain literal; this strips a
// surrounding pair of double quotes when present, and is a no-op otherwise.
func unwrapAttrText(raw string) string {
	if len(raw) >= 2 && raw[0] == '"' && raw[len(raw)-1] == '"' {
		return raw[1 : len(raw)-1]
	}
	return raw
}

// run walks every BusbarSection's outgoing branches, in deterministic (sorted) equipment-id
// order so re-running BuildTopology against the same data always produces the exact same
// connection_key/line_key values (both are content-derived, but stable ordering also makes
// the completedBusbars dedup behavior deterministic).
func (w *walker) run() {
	var busbarIDs []string
	for eqID, info := range w.g.equipment {
		if info.class == "BusbarSection" {
			busbarIDs = append(busbarIDs, eqID)
		}
	}
	sort.Strings(busbarIDs)

	for _, busbarID := range busbarIDs {
		w.walkFromBusbar(busbarID)
		w.completedBusbars[busbarID] = true
	}
}

// walkFromBusbar starts one branch per OTHER equipment sharing the busbar's node, from
// EVERY one of the BusbarSection's own real Terminals (a busbar can have any number of real
// CIM Terminals, one per feeder/connection - see loadGraph's doc comment) - the direct port
// of getLineSegmentsSwitchesAndEndTerminals's first expansion from a BusbarSection source,
// generalized from a single hardcoded terminal to the real per-Terminal multiplicity.
func (w *walker) walkFromBusbar(busbarID string) {
	for _, own := range w.g.ownTerminals[busbarID] {
		neighbors := stableOtherStubs(w.g.nodeStubs[own.node], busbarID)
		if len(neighbors) == 0 {
			// Genuinely isolated real Terminal: nothing else (not even a Fuse/measuring
			// device) is wired to this node at all. Verified against the real domain_model
			// ground truth: such a Terminal gets no `terminal` row whatsoever (unlike a
			// wired-but-dead-ending feeder, e.g. one ending at an unused Fuse/measuring
			// device, which DOES still get its own busbar terminal row even though the walk
			// never completes a connection from it - see completeDeadEnd).
			continue
		}
		w.ensureTerminal(busbarID, own.terminalKey, "BusbarSection")
		for _, nb := range neighbors {
			bs := branchState{startTerminalKey: own.terminalKey, atNode: own.node}
			w.step(bs, nb)
		}
	}
}

// step advances one walk branch through equipment nb (reached from bs.atNode), mirroring
// extendConnections/getLineSegmentsSwitchesAndEndTerminals's per-step logic: check the stop
// conditions first, then either complete the connection or recurse through nb's other side.
func (w *walker) step(bs branchState, nb stub) {
	kind := w.g.classify(nb.equipmentID)
	switch kind {
	case stopBusbar, stopTransformer, stopHouse:
		w.complete(bs, nb, kind)
		return
	}

	// Plain pass-through equipment (Fuse/ACLineSegment/anything else two-terminal): keep
	// walking through its OTHER terminal, contributing no terminal of its own.
	//
	// Switch and (standalone) Junction are the one deliberate exception, mirroring the real
	// connector's createTerminalsAndFeederEnds loop exactly:
	//   if ((io instanceof Switch || io instanceof Junction) && !(io instanceof Fuse)) { ... }
	// - every mid-chain Switch or Junction gets its OWN terminal instance recorded (via
	// ensureTerminal) and referenced by connection_terminal_map, in walk order, between the
	// connection's source and target. Fuse is explicitly excluded, matching the real code -
	// a Fuse mid-chain never gets its own terminal, only a pure pass-through.
	nextBS := bs
	nextBS.lastEquipmentID = nb.equipmentID
	nbClass := w.g.equipment[nb.equipmentID].class
	switch nbClass {
	case "ACLineSegment":
		nextBS.lineSegments = append(append([]string{}, bs.lineSegments...), nb.equipmentID)
	case "Switch", "Junction":
		// Unlike BusbarSection/PowerTransformer (whose terminalKey IS the specific real CIM
		// Terminal id, since the real connector creates one terminal per real Terminal for
		// those), a mid-chain Switch/Junction gets exactly ONE terminal for the whole
		// equipment, named by its bare equipment ID (verified against the real connector's
		// output: e.g. Switch "E-2" and Junction "E-4" terminals are named "E-2"/"E-4", not
		// a raw Terminal id like "E-4-2") - regardless of which of its own real Terminals a
		// given walk branch happened to arrive through. Use nb.equipmentID, not
		// nb.terminalKey, so every branch through the same Junction/Switch (e.g. a 3+-way
		// splice reached from different directions) converges on the same terminal row.
		w.ensureTerminal(nb.equipmentID, nb.equipmentID, nbClass)
		nextBS.midTerminalKeys = append(append([]string{}, bs.midTerminalKeys...), nb.equipmentID)
	}

	if len(nb.otherNodes) == 0 {
		// Single-terminal, non-anchor equipment with no other side - shouldn't normally
		// happen (only BusbarSection is single-terminal, and that's already an anchor
		// classified above), but guard against it defensively as a dead end.
		w.completeDeadEnd(nextBS, nb)
		return
	}

	// Fan out to every other node of this equipment - exactly one for ordinary two-terminal
	// equipment (Fuse/Switch/ACLineSegment/a 2-node Junction splice), 2+ for a multi-way
	// Junction splice (arriving via any one cable end can continue via ANY other cable end,
	// each potentially leading to a wholly different anchor/connection).
	for _, other := range nb.otherNodes {
		branchBS := nextBS
		branchBS.atNode = other
		further := stableOtherStubs(w.g.nodeStubs[other], nb.equipmentID)
		if len(further) == 0 {
			// Genuine dead end: nothing further is wired beyond this node. Verified against
			// the real domain_model ground truth: this is NOT silently dropped - the real
			// connector still creates a connection ending at nb's own OTHER real Terminal
			// instance sitting at this now-childless node (e.g. an ACLineSegment's far end
			// with nothing wired beyond it - real external_id "D-1", name "Dummy Terminal at
			// the cable dead end", NOT_SWITCHABLE, no device). Only if nb genuinely has no
			// such "far" Terminal at all (shouldn't normally happen once otherNodes is
			// non-empty, but guarded defensively) is the branch actually dropped.
			if farStub, ok := ownStubAt(w.g, nb.equipmentID, other); ok {
				w.complete(branchBS, farStub, stopDeadEnd)
			} else {
				w.completeDeadEnd(branchBS, nb)
			}
			continue
		}
		for _, f := range further {
			fClass := w.g.equipment[f.equipmentID].class
			fBS := branchBS
			if len(further) > 1 && nbClass != "Switch" && nbClass != "Junction" && nbClass != "Fuse" &&
				fClass != "Switch" && fClass != "Junction" {
				// Genuine T-tap NOT anchored by a multi-terminal Switch/Junction equipment on
				// EITHER side of this specific branch (a Switch/Junction we're continuing
				// into already anchors the branch point with its own mid-chain terminal -
				// registering nb's far terminal too would double-book the same physical
				// point, which the real connector does not do), and not a Fuse either (Fuse
				// is excluded here for the same reason it's excluded from the Switch/Junction
				// rule - verified against the real domain_model ground truth: a Fuse feeding
				// two downstream cables, e.g. "FEED-6-FU", gets no extra terminal): a plain
				// two-terminal ACLineSegment whose far end coincides with more than one
				// further neighbor, continuing into another plain ACLineSegment (e.g. two
				// ACLineSegments meeting at one node with no explicit Junction object there).
				// Verified against the real domain_model ground truth (e.g.
				// "LIN-J-10-C-17-T1" mid-chain terminal on the C-17<->H-25 walk, which
				// continues into the ACLineSegment "LIN-J-10-H-25" - but is absent on the
				// C-17<->H-20 walk, which instead continues into the Junction "J-10" and
				// therefore does NOT get this extra terminal): the real connector records an
				// extra mid-chain terminal here, using nb's own far-end Terminal instance
				// (the one physically sitting at the branch node), not a synthetic ID.
				if farStub, ok := ownStubAt(w.g, nb.equipmentID, other); ok {
					w.ensureTerminal(farStub.equipmentID, farStub.terminalKey, nbClass)
					fBS.midTerminalKeys = append(append([]string{}, branchBS.midTerminalKeys...), farStub.terminalKey)
				}
			}
			w.step(fBS, f)
		}
	}
}

// ownStubAt finds equipmentID's own real Terminal stub sitting at node (as opposed to a
// neighbor's stub) - used to locate an ACLineSegment/etc.'s own far-end Terminal when a walk
// runs off the edge of the wired model there (see step's dead-end handling).
func ownStubAt(g *graph, equipmentID, node string) (stub, bool) {
	for _, s := range g.ownTerminals[equipmentID] {
		if s.node == node {
			return s, true
		}
	}
	return stub{}, false
}

// stableOtherStubs returns every stub at a node except the one belonging to excludeEquipment
// (the equipment we just arrived via - never walk straight back the way we came), in a
// deterministic order so repeated runs are reproducible.
func stableOtherStubs(stubs []stub, excludeEquipment string) []stub {
	var out []stub
	for _, s := range stubs {
		if s.equipmentID == excludeEquipment {
			continue
		}
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].terminalKey < out[j].terminalKey })
	return out
}

// ensureTerminal registers (or re-confirms) a terminal row for equipment eqID's specific
// terminal instance terminalKey, deriving network_device_id/device_kind/feeder_area/type
// exactly like the existing jag2nsc_terminal helper view's CASE logic (kept consistent on purpose).
func (w *walker) ensureTerminal(eqID, terminalKey, class string) {
	if _, ok := w.result.terminals[terminalKey]; ok {
		return
	}
	info := w.g.equipment[eqID]
	deviceKind, deviceExtID := "", ""
	switch class {
	case "BusbarSection":
		deviceKind, deviceExtID = "BUSBAR", info.containerID
	case "PowerTransformer":
		deviceKind, deviceExtID = "TRANSFORMER", eqID
	default:
		if w.g.container[info.containerID] == "house" {
			deviceKind, deviceExtID = "HOUSE_CONNECTION", info.containerID
		}
	}
	feederAreaExtID := ""
	if class != "PowerTransformer" && w.g.container[info.containerID] == "bay" {
		feederAreaExtID = info.containerID
	}
	terminalType := "NOT_SWITCHABLE"
	switch class {
	case "Fuse":
		terminalType = "FUSE"
	case "Switch":
		terminalType = "SWITCH"
	}
	w.result.terminals[terminalKey] = terminalRow{
		terminalKey:        terminalKey,
		equipmentID:        eqID,
		cimClass:           class,
		networkDeviceExtID: deviceExtID,
		deviceKind:         deviceKind,
		feederAreaExtID:    feederAreaExtID,
		terminalType:       terminalType,
	}
}

// complete finalizes a walk branch that reached an anchor (BusbarSection/PowerTransformer/
// house), emitting the connection + connection_terminal_map + line_segment rows. Mirrors
// CondensedCimMapper.createConnection/createLineSegments and the real connector's
// isAlreadyProcessed dedup for the busbar<->busbar case (see package doc comment).
func (w *walker) complete(bs branchState, endStub stub, kind stopKind) {
	if kind == stopBusbar && w.completedBusbars[endStub.equipmentID] {
		return // the reverse direction of this same physical link was already recorded
	}

	endClass := w.g.equipment[endStub.equipmentID].class
	w.ensureTerminal(endStub.equipmentID, endStub.terminalKey, endClass)

	startTerm := w.result.terminals[bs.startTerminalKey]
	sourceKind, sourceExtID := w.deviceKindFor(startTerm)
	targetKind, targetExtID := w.g.deviceKindAndID(kind, endStub.equipmentID)
	_ = sourceExtID
	_ = targetExtID

	w.connSeq++
	connKey := fmt.Sprintf("%s~%s~%d", bs.startTerminalKey, endStub.terminalKey, w.connSeq)
	w.result.connections = append(w.result.connections, connectionRow{
		connectionKey:     connKey,
		externalID:        connKey,
		sourceTerminalKey: bs.startTerminalKey,
		targetTerminalKey: endStub.terminalKey,
		sourceDeviceKind:  sourceKind,
		targetDeviceKind:  targetKind,
		midTerminalKeys:   bs.midTerminalKeys,
	})
	for i, aclID := range bs.lineSegments {
		w.result.lineSegs = append(w.result.lineSegs, lineSegmentRow{
			lineKey:           fmt.Sprintf("%s~seg~%d", connKey, i+1),
			connectionKey:     connKey,
			aclineEquipmentID: aclID,
			sequenceNumber:    i + 1,
		})
	}
}

// completeDeadEnd is the true no-op fallback: a walk ran off the edge of the model with no
// own far-end Terminal at all to attribute it to (should not normally happen once an
// equipment has otherNodes, but guarded defensively - see step's dead-end handling, which
// normally instead calls complete(..., stopDeadEnd) using the equipment's own far Terminal).
func (w *walker) completeDeadEnd(bs branchState, endStub stub) {
	_ = bs
	_ = endStub
}

func (w *walker) deviceKindFor(t terminalRow) (kind, extID string) {
	return t.deviceKind, t.networkDeviceExtID
}

// writeTopology performs the full-replace write: every registered terminal (including
// dead-end BusbarSection/Switch/Junction terminals with no connection - see the write loop
// below), every connection, and every line_segment, all inside one transaction so a
// concurrent reader never observes a half-written topology.
func writeTopology(ctx context.Context, db *sql.DB, result *topologyResult) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	for _, stmt := range []string{
		`TRUNCATE jag2nsc_topo_line_segment`,
		`TRUNCATE jag2nsc_topo_connection_terminal_map`,
		`TRUNCATE jag2nsc_topo_connection CASCADE`,
		`TRUNCATE jag2nsc_topo_terminal CASCADE`,
	} {
		if _, err := tx.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("%s: %w", stmt, err)
		}
	}

	termStmt, err := tx.PrepareContext(ctx, `
INSERT INTO jag2nsc_topo_terminal (terminal_key, equipment_id, cim_class, network_device_external_id, device_kind, feeder_area_external_id, terminal_type)
VALUES ($1, $2, $3, NULLIF($4, ''), NULLIF($5, ''), NULLIF($6, ''), $7)`)
	if err != nil {
		return err
	}
	defer termStmt.Close()

	// sequence_number within connection_terminal_map: 1=source, 2=target (the walk never
	// produces more than these two referenced terminal instances per connection in this
	// implementation - see package doc comment's "known deliberate simplifications").
	ctmStmt, err := tx.PrepareContext(ctx, `
INSERT INTO jag2nsc_topo_connection_terminal_map (connection_key, terminal_key, sequence_number) VALUES ($1, $2, $3)`)
	if err != nil {
		return err
	}
	defer ctmStmt.Close()

	connStmt, err := tx.PrepareContext(ctx, `
INSERT INTO jag2nsc_topo_connection (connection_key, external_id, source_terminal_key, target_terminal_key, source_device_kind, target_device_kind)
VALUES ($1, $2, $3, $4, NULLIF($5, ''), NULLIF($6, ''))`)
	if err != nil {
		return err
	}
	defer connStmt.Close()

	lineStmt, err := tx.PrepareContext(ctx, `
INSERT INTO jag2nsc_topo_line_segment (line_key, connection_key, acline_equipment_id, sequence_number) VALUES ($1, $2, $3, $4)`)
	if err != nil {
		return err
	}
	defer lineStmt.Close()

	written := map[string]bool{}
	writeTerminalIfNeeded := func(key string) error {
		if written[key] {
			return nil
		}
		t, ok := result.terminals[key]
		if !ok {
			return fmt.Errorf("internal error: referenced terminal %q was never registered", key)
		}
		if _, err := termStmt.ExecContext(ctx, t.terminalKey, t.equipmentID, t.cimClass, t.networkDeviceExtID, t.deviceKind, t.feederAreaExtID, t.terminalType); err != nil {
			return err
		}
		written[key] = true
		return nil
	}

	// Write every registered terminal up front, not only ones referenced by a connection.
	// Verified against the real domain_model ground truth: a BusbarSection's own real
	// Terminal that leads nowhere (an unused/disabled feeder, e.g. "Feeder 7 ... Disabled (no
	// measuring device)") still gets a `terminal` row there - only the `connection` for a
	// dead-end walk is dropped (see completeDeadEnd), not the anchor's own terminal. Sorted
	// keys make the write order (and therefore this table's insert order) deterministic.
	sortedKeys := make([]string, 0, len(result.terminals))
	for k := range result.terminals {
		sortedKeys = append(sortedKeys, k)
	}
	sort.Strings(sortedKeys)
	for _, k := range sortedKeys {
		if err := writeTerminalIfNeeded(k); err != nil {
			return err
		}
	}

	for _, c := range result.connections {
		if err := writeTerminalIfNeeded(c.sourceTerminalKey); err != nil {
			return err
		}
		for _, mk := range c.midTerminalKeys {
			if err := writeTerminalIfNeeded(mk); err != nil {
				return err
			}
		}
		if err := writeTerminalIfNeeded(c.targetTerminalKey); err != nil {
			return err
		}
		if _, err := connStmt.ExecContext(ctx, c.connectionKey, c.externalID, c.sourceTerminalKey, c.targetTerminalKey, c.sourceDeviceKind, c.targetDeviceKind); err != nil {
			return err
		}
		// sequence_number: 1=source, 2..N=mid-chain Switch/Junction terminals in walk order,
		// N+1=target - mirrors the real connector's createTerminalConnection numbering.
		seq := 1
		if _, err := ctmStmt.ExecContext(ctx, c.connectionKey, c.sourceTerminalKey, seq); err != nil {
			return err
		}
		for _, mk := range c.midTerminalKeys {
			seq++
			if _, err := ctmStmt.ExecContext(ctx, c.connectionKey, mk, seq); err != nil {
				return err
			}
		}
		seq++
		if _, err := ctmStmt.ExecContext(ctx, c.connectionKey, c.targetTerminalKey, seq); err != nil {
			return err
		}
	}
	for _, l := range result.lineSegs {
		if _, err := lineStmt.ExecContext(ctx, l.lineKey, l.connectionKey, l.aclineEquipmentID, l.sequenceNumber); err != nil {
			return err
		}
	}

	return tx.Commit()
}
