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
func ResolveTerminals(store staging.Store, version uint64, chunkSize int) (map[string]EquipmentTerminals, map[string]bool, []Anomaly, error) {
	byEquipment, err := scanTerminals(store, version, chunkSize)
	if err != nil {
		return nil, nil, nil, err
	}
	nodeRoleIDs, err := scanNodeRoleIDs(store, version, chunkSize)
	if err != nil {
		return nil, nil, nil, err
	}

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

	missing, err := findEquipmentWithoutTerminals(store, version, chunkSize, byEquipment)
	if err != nil {
		return result, nodeRoleIDs, anomalies, err
	}
	for _, eqID := range missing {
		anomalies = append(anomalies, Anomaly{EquipmentID: eqID, Message: "equipment has zero terminals"})
	}

	return result, nodeRoleIDs, anomalies, nil
}

// scanNodeRoleIDs scans every class in nodeRoleClasses (currently just
// "Junction") and returns the distinct object IDs found — cheap compared
// to a per-Equipment class lookup, since there are normally few node-role
// classes and few objects of each.
func scanNodeRoleIDs(store staging.Store, version uint64, chunkSize int) (map[string]bool, error) {
	ids := map[string]bool{}
	for class := range nodeRoleClasses {
		afterID := ""
		for {
			records, err := store.GetByClass(version, class, afterID, chunkSize)
			if err != nil {
				return nil, fmt.Errorf("common: scanning node-role class %s: %w", class, err)
			}
			if len(records) == 0 {
				break
			}
			distinct := distinctIDsInOrder(records)
			for _, id := range distinct {
				ids[id] = true
			}
			afterID = distinct[len(distinct)-1]
			if len(distinct) < chunkSize {
				break
			}
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

// findEquipmentWithoutTerminals scans every class other than "Terminal" in
// the version and returns the IDs of objects that carry an
// Equipment.EquipmentContainer attribute (the generic "this is Equipment"
// marker) but never showed up in the Terminal scan — i.e. Equipment with
// zero Terminals. PowerElectronicsUnit subclasses (NSC dialect: Wallbox,
// PhotoVoltaicUnit, BatteryUnit, AirConditioningUnit, ...) are excluded via
// their PowerElectronicsUnit.PowerElectronicsConnection attribute (decided
// 2026-07-14): analogous to GeneratingUnit above, these are satellite device
// descriptions attached to their PowerElectronicsConnection (the actual
// grid-connection point with its own Terminal), never wired directly.
func findEquipmentWithoutTerminals(store staging.Store, version uint64, chunkSize int, found map[string][]rawTerminal) ([]string, error) {
	classes, err := store.ListClasses(version)
	if err != nil {
		return nil, fmt.Errorf("common: listing classes: %w", err)
	}

	var missing []string
	for _, class := range classes {
		if class == "Terminal" || isGeneratingUnitClass(class) {
			continue
		}

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
	}
	sort.Strings(missing)
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
