// Package common contains shared data structures and logic used by more
// than one /internal/impl subpackage (see Impl.md). This file implements
// the second step of Phase 2 reference resolution (see Konzept.md):
// resolving CIM Terminal objects into a per-Equipment ConnectivityNode
// mapping, which is what later steps use to build model.Node/model.Edge.
package common

import (
	"fmt"
	"sort"
	"strings"
	"sync"

	"gitlab.com/openk-nsc/jag/internal/core/staging"
	"gitlab.com/openk-nsc/jag/internal/importer/model"
)

// EquipmentTerminals holds the ConnectivityNode IDs found for one piece of
// Equipment, keyed by ACDCTerminal.sequenceNumber (1 or 2). Equipment with
// only Node1 set is a single-terminal source/sink (see Konzept.md/model
// decision: connection 2 is implicitly wired to GND during model-building,
// not stored as a real Terminal in the source data).
//
// ExtraNodes is populated only for node-role Equipment (see nodeRoleClasses
// — currently Junction) that has more than one of its own ConnectivityNode:
// unlike a Zweipol, such Equipment isn't limited to two Terminals — a
// branching splice/T-Muffe can have any number of connections, all
// representing the SAME physical point. Node1 holds one of them
// (the lexicographically smallest), ExtraNodes the rest; junctionmerge.go's
// MergeJunctionNodes unifies them onto one canonical Node ID. Regular
// Zweipol/single-terminal-source Equipment never sets this field.
type EquipmentTerminals struct {
	EquipmentID string
	Node1       string // ConnectivityNode ID at sequenceNumber 1 (or TopologicalNode ID, bus-branch fallback — see scanTerminals)
	Node2       string // ConnectivityNode ID at sequenceNumber 2, if any (empty for single-terminal source/sink)
	ExtraNodes  []string
}

// nodeRoleClasses are CIM classes whose Equipment objects are, despite
// having their own Terminals, not a Zweipol/Edge at all — they are purely a
// Node-role marker for their own (possibly several) ConnectivityNode(s).
// Decided explicitly with the user (2026-07-13, NSC import investigation):
// a Junction (Kabelmuffe) can be a branching splice (Abzweigmuffe/T-Muffe)
// with 3+ Terminals, one per cable segment meeting at that physical point —
// structurally impossible to express as a two-terminal Edge. This mirrors
// BusbarSection's existing Node-role treatment (see nodeedge.go), just
// generalized to more than one of the object's own ConnectivityNodes. This
// is dialect-neutral: it applies to CGMES data too, not just NSC, since
// it's a real modeling decision, not an import-format quirk.
//
// BusbarSection (added 2026-07-14, load-test investigation): a busbar with
// many feeder connections naturally has one Terminal per feeder (observed:
// 11 Terminals for a 200-station/10-feeder load-test dataset). Without
// BusbarSection in nodeRoleClasses, classifyTerminals rejected every such
// busbar as an Anomaly (>2 Terminals) before it ever reached ResolveTerminals'
// result map, so nodeedge.go's separate nodeOnlyIDs/BusbarSection handling
// (see its own doc comment) never got the chance to treat it as a Node-role
// marker — this fixes that upstream gap. Same reasoning as Junction: a
// busbar's own Node1/ExtraNodes are its (possibly several) real
// ConnectivityNode(s), never true Edge terminals.
var nodeRoleClasses = map[string]bool{
	"Junction":      true,
	"BusbarSection": true,
}

// TerminalRef is one raw Terminal seen for an Equipment, kept for
// diagnostics on Anomaly.
type TerminalRef struct {
	TerminalID       string
	SequenceNumber   string // raw attribute value, kept as string (may be missing/non-numeric)
	ConnectivityNode string
}

// Anomaly describes an Equipment whose Terminal count/sequencing didn't
// match the expected "1 or 2 terminals, seq 1/2, each with a
// ConnectivityNode" shape (see Idee.md's import-time invariant "every
// element has exactly two Terminals, except single-terminal source/sink
// equipment"). Collected instead of aborting resolution (Idee.md Phase 4:
// run to completion, gather all errors).
type Anomaly struct {
	EquipmentID string
	Terminals   []TerminalRef
	Message     string
}

// rawTerminal is one Terminal attributed to an Equipment during the scan,
// before it has been classified into EquipmentTerminals or an Anomaly.
type rawTerminal struct {
	TerminalRef
}

// DefaultTerminalScanWorkers is the default concurrency for the per-class
// scans inside ResolveTerminalsParallel (step (a) of the parallel-import
// decision, 2026-07-14 — see this file's and parallel.go's doc comments).
// Only used as the fallback when workers <= 0 is passed.
const DefaultTerminalScanWorkers = 8

// ResolveTerminals scans the "Terminal" class of the given staging version
// in cursor-based chunks of chunkSize distinct Terminal IDs (see
// staging.Store.GetByClass), resolving each Terminal's ConductingEquipment +
// ConnectivityNode + sequenceNumber and accumulating them per Equipment.
//
// Terminal alone cannot prove completeness: an Equipment with ZERO
// Terminals never appears in the Terminal scan at all, so it would
// otherwise vanish silently instead of being reported as the import-time
// invariant violation it is (Idee.md: "an element without exactly two
// Terminals is an error"). To catch this, ResolveTerminals also scans every
// other class present in the version for objects carrying an
// Equipment.EquipmentContainer attribute (the generic marker of "this is a
// real piece of Equipment", present regardless of CIM subclass) and cross-
// checks them against what the Terminal scan found.
//
// The returned nodeRoleIDs set names exactly the Equipment IDs resolved via
// the node-role path (nodeRoleClasses) rather than the ordinary Zweipol
// path — callers (BuildNodesAndEdges, MergeJunctionNodes) need it to tell
// the two apart.
//
// This is a thin wrapper around ResolveTerminalsParallel using
// DefaultTerminalScanWorkers — kept as a separate entry point so existing
// callers/tests that don't care about the worker count don't need to
// change.
func ResolveTerminals(store staging.Store, version uint64, chunkSize int) (map[string]EquipmentTerminals, map[string]bool, []Anomaly, error) {
	return ResolveTerminalsParallel(store, version, chunkSize, 0)
}

// ResolveTerminalsParallel is ResolveTerminals with an explicit worker
// count (workers <= 0 defaults to DefaultTerminalScanWorkers) — "step (a)"
// of the two-step parallel-import plan (see parallel.go's
// BuildSachdatenAndGeometryParallel for "step (b)", done first per explicit
// user decision).
//
// Unlike step (b), Terminal resolution has no natural per-station shape to
// split by (a single "Terminal" class scan has no station boundary to cut
// along without a new range-partitioned Store API, which was deliberately
// NOT added here — see this function's doc comment on scanTerminals
// staying sequential). What IS trivially parallel, and previously ran
// strictly one class after another for no data-dependency reason, is:
//   - the "equipment with zero Terminals" cross-check
//     (findEquipmentWithoutTerminalsParallel), which scans every OTHER
//     class in the version once each — each class's scan is fully
//     independent of every other, only their findings get merged at the
//     end;
//   - scanNodeRoleIDsParallel, same reasoning across nodeRoleClasses;
//   - and the in-memory classification pass (classifyAll) can run
//     concurrently WITH the zero-Terminal cross-check, since both only
//     read the already-fully-scanned byEquipment map (built by
//     scanTerminals, which must still finish first) and produce
//     independent outputs.
//
// scanTerminals itself (the "Terminal" class scan) stays exactly as before
// — one single sequential cursor scan. Splitting that one further would
// need a range-partitioned GetByClass variant on staging.Store (an
// interface change), which is out of scope for this step; if the Terminal
// scan itself turns out to be the dominant cost at 30-40GB scale after
// measuring, that is the next thing to revisit — not assumed here.
func ResolveTerminalsParallel(store staging.Store, version uint64, chunkSize int, workers int) (map[string]EquipmentTerminals, map[string]bool, []Anomaly, error) {
	if workers <= 0 {
		workers = DefaultTerminalScanWorkers
	}

	byEquipment, err := scanTerminals(store, version, chunkSize)
	if err != nil {
		return nil, nil, nil, err
	}
	nodeRoleIDs, err := scanNodeRoleIDsParallel(store, version, chunkSize, workers)
	if err != nil {
		return nil, nil, nil, err
	}

	var result map[string]EquipmentTerminals
	var classifyAnomalies []Anomaly
	var missing []string
	var missingErr error
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		result, classifyAnomalies = classifyAll(byEquipment, nodeRoleIDs)
	}()
	go func() {
		defer wg.Done()
		missing, missingErr = findEquipmentWithoutTerminalsParallel(store, version, chunkSize, byEquipment, workers)
	}()
	wg.Wait()
	if missingErr != nil {
		return result, nodeRoleIDs, classifyAnomalies, missingErr
	}

	anomalies := classifyAnomalies
	for _, eqID := range missing {
		anomalies = append(anomalies, Anomaly{EquipmentID: eqID, Message: "equipment has zero terminals"})
	}
	return result, nodeRoleIDs, anomalies, nil
}

// classifyAll runs the pure in-memory classification pass (no DB access)
// over every Equipment found by scanTerminals, producing the final
// EquipmentTerminals result map plus classification Anomalies. Split out
// of ResolveTerminalsParallel so it can run concurrently with
// findEquipmentWithoutTerminalsParallel (see that function's doc comment).
func classifyAll(byEquipment map[string][]rawTerminal, nodeRoleIDs map[string]bool) (map[string]EquipmentTerminals, []Anomaly) {
	result := map[string]EquipmentTerminals{}
	var anomalies []Anomaly

	var equipmentIDs []string
	for eqID := range byEquipment {
		equipmentIDs = append(equipmentIDs, eqID)
	}
	sort.Strings(equipmentIDs)

	p := newProgress("terminals-classify")
	for _, eqID := range equipmentIDs {
		var et EquipmentTerminals
		var ok bool
		var msg string
		if nodeRoleIDs[eqID] {
			et, ok, msg = classifyNodeRoleTerminals(eqID, byEquipment[eqID])
		} else {
			et, ok, msg = classifyTerminals(eqID, byEquipment[eqID])
		}
		if !ok {
			refs := make([]TerminalRef, len(byEquipment[eqID]))
			for i, t := range byEquipment[eqID] {
				refs[i] = t.TerminalRef
			}
			anomalies = append(anomalies, Anomaly{EquipmentID: eqID, Terminals: refs, Message: msg})
			p.Tick(1)
			continue
		}
		result[eqID] = et
		p.Tick(1)
	}
	p.Done()
	return result, anomalies
}

// scanClassIDs performs one chunked class scan (like scanClass in
// container.go, duplicated here to avoid a cross-file dependency on its
// exact return shape) and returns the distinct object IDs found, in ID
// order.
func scanClassIDs(store staging.Store, version uint64, chunkSize int, class string) ([]string, error) {
	var ids []string
	afterID := ""
	for {
		records, err := store.GetByClass(version, class, afterID, chunkSize)
		if err != nil {
			return nil, fmt.Errorf("common: scanning class %s: %w", class, err)
		}
		if len(records) == 0 {
			break
		}
		distinct := distinctIDsInOrder(records)
		ids = append(ids, distinct...)
		afterID = distinct[len(distinct)-1]
		if len(distinct) < chunkSize {
			break
		}
	}
	return ids, nil
}

// scanNodeRoleIDsParallel scans every class in nodeRoleClasses (currently
// Junction/BusbarSection) concurrently — one goroutine per class, bounded
// by workers — and returns the union of distinct object IDs found. Cheap
// in absolute terms (there are normally few node-role classes and few
// objects of each), but kept consistent with the same concurrent-per-class
// pattern used for the much larger findEquipmentWithoutTerminalsParallel
// scan below.
func scanNodeRoleIDsParallel(store staging.Store, version uint64, chunkSize int, workers int) (map[string]bool, error) {
	var classes []string
	for class := range nodeRoleClasses {
		classes = append(classes, class)
	}

	type classResult struct {
		ids []string
		err error
	}
	results := make([]classResult, len(classes))
	sem := make(chan struct{}, workers)
	var wg sync.WaitGroup
	for i, class := range classes {
		wg.Add(1)
		sem <- struct{}{}
		go func(i int, class string) {
			defer wg.Done()
			defer func() { <-sem }()
			ids, err := scanClassIDs(store, version, chunkSize, class)
			results[i] = classResult{ids: ids, err: err}
		}(i, class)
	}
	wg.Wait()

	ids := map[string]bool{}
	for _, r := range results {
		if r.err != nil {
			return nil, fmt.Errorf("common: scanning node-role classes: %w", r.err)
		}
		for _, id := range r.ids {
			ids[id] = true
		}
	}
	return ids, nil
}

// scanTerminals performs the actual chunked "Terminal" class scan and
// groups the raw Terminals by the Equipment they belong to.
func scanTerminals(store staging.Store, version uint64, chunkSize int) (map[string][]rawTerminal, error) {
	byEquipment := map[string][]rawTerminal{}

	p := newProgress("terminals-scan")
	afterID := ""
	for {
		records, err := store.GetByClass(version, "Terminal", afterID, chunkSize)
		if err != nil {
			return nil, fmt.Errorf("common: scanning terminals: %w", err)
		}
		if len(records) == 0 {
			break
		}

		idx := BuildObjectIndex(records)
		ids := distinctIDsInOrder(records)

		for _, tID := range ids {
			eqID := idx.Ref(tID, "Terminal.ConductingEquipment")
			node := idx.Ref(tID, "Terminal.ConnectivityNode")
			if node == "" {
				// Pure bus-branch CGMES sources have no ConnectivityNode
				// layer at all (see Konzept.md's "CGMES kennt zwei
				// grundverschiedene Modellvarianten"): the Terminal's node
				// reference is carried directly as Terminal.TopologicalNode
				// (TP profile) instead. JAG has no finer physical layer to
				// recover there, so it falls back to using the
				// TopologicalNode ID as the Node identity directly — this
				// is the already-decided, already-electrically-reduced
				// view for such sources, not a guess.
				node = idx.Ref(tID, "Terminal.TopologicalNode")
			}
			seq := idx.Attr(tID, "ACDCTerminal.sequenceNumber")

			// A malformed Terminal without ConductingEquipment has no
			// Equipment to attach to — report it under its own ID instead
			// of silently dropping it.
			key := eqID
			if key == "" {
				key = tID
			}
			byEquipment[key] = append(byEquipment[key], rawTerminal{
				TerminalRef{TerminalID: tID, SequenceNumber: seq, ConnectivityNode: node},
			})
		}

		afterID = ids[len(ids)-1]
		p.Tick(len(ids))
		if len(ids) < chunkSize {
			break
		}
	}
	p.Done()
	return byEquipment, nil
}

// isGeneratingUnitClass reports whether class is CIM's GeneratingUnit or one
// of its energy-source subclasses (Thermal/Hydro/Solar/Wind/Nuclear...).
// These carry Equipment.EquipmentContainer (like real Equipment) but are, by
// CIM design, never wired via their own Terminal — they are satellite
// metadata (rated power, fuel type, control-area grouping) attached to the
// SynchronousMachine that references them via RotatingMachine.GeneratingUnit
// (see the SynchronousMachine's own single Terminal + implicit GND second
// connection). Decided explicitly with the user rather than inferred
// structurally, since it's a small, stable, known CIM naming pattern.
func isGeneratingUnitClass(class string) bool {
	return strings.HasSuffix(class, "GeneratingUnit")
}

// findEquipmentWithoutTerminalsParallel scans every class other than
// "Terminal" in the version and returns the IDs of objects that carry an
// Equipment.EquipmentContainer attribute (the generic "this is Equipment"
// marker) but never showed up in the Terminal scan — i.e. Equipment with
// zero Terminals. PowerElectronicsUnit subclasses (NSC dialect: Wallbox,
// PhotoVoltaicUnit, BatteryUnit, AirConditioningUnit, ...) are excluded via
// their PowerElectronicsUnit.PowerElectronicsConnection attribute (decided
// 2026-07-14): analogous to GeneratingUnit above, these are satellite
// device descriptions attached to their PowerElectronicsConnection (the
// actual grid-connection point with its own Terminal), never wired
// directly.
//
// Each class's scan is fully independent of every other's (found is
// read-only here, already fully populated by scanTerminals) — this runs
// them concurrently across up to `workers` goroutines at once instead of
// one class after another, which is how it worked before "step (a)" of
// the parallel-import plan.
func findEquipmentWithoutTerminalsParallel(store staging.Store, version uint64, chunkSize int, found map[string][]rawTerminal, workers int) ([]string, error) {
	classes, err := store.ListClasses(version)
	if err != nil {
		return nil, fmt.Errorf("common: listing classes: %w", err)
	}

	var scanClasses []string
	for _, class := range classes {
		if class == "Terminal" || isGeneratingUnitClass(class) {
			continue
		}
		scanClasses = append(scanClasses, class)
	}

	type classResult struct {
		missing []string
		err     error
	}
	results := make([]classResult, len(scanClasses))
	sem := make(chan struct{}, workers)
	var wg sync.WaitGroup
	for i, class := range scanClasses {
		wg.Add(1)
		sem <- struct{}{}
		go func(i int, class string) {
			defer wg.Done()
			defer func() { <-sem }()
			m, err := scanClassMissingEquipment(store, version, chunkSize, class, found)
			results[i] = classResult{missing: m, err: err}
		}(i, class)
	}
	wg.Wait()

	var missing []string
	for _, r := range results {
		if r.err != nil {
			return nil, r.err
		}
		missing = append(missing, r.missing...)
	}
	sort.Strings(missing)
	return missing, nil
}

// scanClassMissingEquipment performs one chunked class scan for `class`
// and returns the IDs of Equipment objects (per the
// Equipment.EquipmentContainer marker) not present in `found`. Factored
// out of findEquipmentWithoutTerminalsParallel so it can run as one
// goroutine's unit of work.
func scanClassMissingEquipment(store staging.Store, version uint64, chunkSize int, class string, found map[string][]rawTerminal) ([]string, error) {
	var missing []string
	afterID := ""
	for {
		records, err := store.GetByClass(version, class, afterID, chunkSize)
		if err != nil {
			return nil, fmt.Errorf("common: scanning class %s: %w", class, err)
		}
		if len(records) == 0 {
			break
		}

		idx := BuildObjectIndex(records)
		ids := distinctIDsInOrder(records)

		for _, id := range ids {
			if !idx.HasAttr(id, "Equipment.EquipmentContainer") {
				continue
			}
			if idx.HasAttr(id, "PowerElectronicsUnit.PowerElectronicsConnection") {
				continue
			}
			if _, ok := found[id]; !ok {
				missing = append(missing, id)
			}
		}

		afterID = ids[len(ids)-1]
		if len(ids) < chunkSize {
			break
		}
	}
	return missing, nil
}

// classifyTerminals interprets the raw Terminals collected for one
// Equipment. Exactly 1 or 2 terminals with sequence numbers "1"/"2" and a
// non-empty ConnectivityNode is the expected shape; anything else
// (0 terminals, >2, missing/duplicate sequence numbers, missing
// ConnectivityNode) is reported as an Anomaly instead of guessed at.
func classifyTerminals(eqID string, terms []rawTerminal) (EquipmentTerminals, bool, string) {
	if len(terms) == 0 || len(terms) > 2 {
		return EquipmentTerminals{}, false, fmt.Sprintf("expected 1 or 2 terminals, found %d", len(terms))
	}

	et := EquipmentTerminals{EquipmentID: eqID}
	seen := map[string]string{} // sequenceNumber -> ConnectivityNode
	for _, t := range terms {
		if t.ConnectivityNode == "" {
			return EquipmentTerminals{}, false, fmt.Sprintf("terminal %s has no ConnectivityNode and no TopologicalNode", t.TerminalID)
		}
		if _, dup := seen[t.SequenceNumber]; dup {
			return EquipmentTerminals{}, false, fmt.Sprintf("duplicate sequenceNumber %q", t.SequenceNumber)
		}
		seen[t.SequenceNumber] = t.ConnectivityNode
	}

	switch len(terms) {
	case 1:
		node, ok := seen["1"]
		if !ok {
			return EquipmentTerminals{}, false, "single terminal has sequenceNumber != 1"
		}
		et.Node1 = node
	case 2:
		n1, ok1 := seen["1"]
		n2, ok2 := seen["2"]
		if !ok1 || !ok2 {
			return EquipmentTerminals{}, false, "two terminals but sequence numbers aren't exactly 1 and 2"
		}
		et.Node1, et.Node2 = n1, n2
	}
	return et, true, ""
}

// classifyNodeRoleTerminals interprets the raw Terminals collected for a
// node-role Equipment (see nodeRoleClasses — currently Junction). Unlike a
// Zweipol, such Equipment may have any number (>=1) of Terminals, each
// wired to a different ConnectivityNode/TopologicalNode, all representing
// the SAME physical connection point (e.g. a branching splice/T-Muffe
// feeding several cable segments) — junctionmerge.go's MergeJunctionNodes
// unifies them onto one canonical Node. Sequence numbers are irrelevant
// here (a multi-way splice has no "terminal 1 vs terminal 2" direction),
// so — unlike classifyTerminals — they are not checked at all. Zero
// Terminals or a missing ConnectivityNode/TopologicalNode on any of them is
// still reported as an Anomaly.
func classifyNodeRoleTerminals(eqID string, terms []rawTerminal) (EquipmentTerminals, bool, string) {
	if len(terms) == 0 {
		return EquipmentTerminals{}, false, "node-role equipment has zero terminals"
	}

	seen := map[string]bool{}
	var nodes []string
	for _, t := range terms {
		if t.ConnectivityNode == "" {
			return EquipmentTerminals{}, false, fmt.Sprintf("terminal %s has no ConnectivityNode and no TopologicalNode", t.TerminalID)
		}
		if !seen[t.ConnectivityNode] {
			seen[t.ConnectivityNode] = true
			nodes = append(nodes, t.ConnectivityNode)
		}
	}
	sort.Strings(nodes)

	et := EquipmentTerminals{EquipmentID: eqID, Node1: nodes[0]}
	if len(nodes) > 1 {
		et.ExtraNodes = nodes[1:]
	}
	return et, true, ""
}

// distinctIDsInOrder returns the distinct object IDs in records, preserving
// their first-seen order (records arrive pre-sorted by ID from
// staging.Store.GetByClass, so this is also ID-ascending order).
func distinctIDsInOrder(records []model.StagingRecord) []string {
	var ids []string
	seen := map[string]bool{}
	for _, r := range records {
		if !seen[r.ID] {
			seen[r.ID] = true
			ids = append(ids, r.ID)
		}
	}
	return ids
}

// ResolveTerminalsForIDs is a TARGETED counterpart to
// ResolveTerminalsParallel: instead of one sequential scan over the whole
// "Terminal" class (the full-model-sized RAM cost documented in
// Konzept.md's "Offene Punkte" RAM section), it resolves Terminal->Node
// info for only the given equipmentIDs, via the same batched/indexed
// lookup style as BuildAttributes/BuildGeometry (see batch.go) — cost/RAM
// scales with len(equipmentIDs), never with total model size.
//
// Added 2026-07-15 for container.go's top-down container-membership
// resolution (see BuildContainers' doc comment): a few callers only ever
// need Terminal info for a small, already-known subset of equipment
// (ACLineSegment chain topology, Junction's ConnectivityNode-container
// fallback) — for those, a full Terminal-class scan is unnecessary and
// exactly the kind of full-model RAM cost the project's resource goals
// (Idee.md) rule out. nodeRoleIDs marks which of equipmentIDs must be
// classified via classifyNodeRoleTerminals instead of classifyTerminals
// (see classifyAll) — pass nil/empty if none of them are node-role
// Equipment (e.g. for a pure ACLineSegment lookup).
func ResolveTerminalsForIDs(store staging.Store, version uint64, equipmentIDs []string, nodeRoleIDs map[string]bool) (map[string]EquipmentTerminals, []Anomaly, error) {
	if len(equipmentIDs) == 0 {
		return map[string]EquipmentTerminals{}, nil, nil
	}

	refsByEquipment, err := getReferencesToAnyIndexed(store, version, equipmentIDs)
	if err != nil {
		return nil, nil, fmt.Errorf("common: fetching Terminal references for %d equipment IDs: %w", len(equipmentIDs), err)
	}

	var terminalIDs []string
	eqOfTerminal := map[string]string{}
	for eqID, refs := range refsByEquipment {
		for _, r := range refs {
			if r.Class == "Terminal" && r.Attribute == "Terminal.ConductingEquipment" {
				terminalIDs = append(terminalIDs, r.ID)
				eqOfTerminal[r.ID] = eqID
			}
		}
	}

	termRecords, err := getByIDsIndexed(store, version, terminalIDs)
	if err != nil {
		return nil, nil, fmt.Errorf("common: fetching %d Terminal objects: %w", len(terminalIDs), err)
	}
	var flat []model.StagingRecord
	for _, recs := range termRecords {
		flat = append(flat, recs...)
	}
	idx := BuildObjectIndex(flat)

	byEquipment := map[string][]rawTerminal{}
	for _, tID := range terminalIDs {
		eqID := eqOfTerminal[tID]
		node := idx.Ref(tID, "Terminal.ConnectivityNode")
		if node == "" {
			// Bus-branch CGMES fallback, see scanTerminals' identical
			// handling.
			node = idx.Ref(tID, "Terminal.TopologicalNode")
		}
		seq := idx.Attr(tID, "ACDCTerminal.sequenceNumber")
		byEquipment[eqID] = append(byEquipment[eqID], rawTerminal{
			TerminalRef{TerminalID: tID, SequenceNumber: seq, ConnectivityNode: node},
		})
	}

	result, anomalies := classifyAll(byEquipment, nodeRoleIDs)
	return result, anomalies, nil
}
