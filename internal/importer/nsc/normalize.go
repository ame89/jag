// Package nsc contains Phase 1 import preprocessing specific to the NSC
// CIM dialect (see examples/nsc/, spec/Idee.md's "Daten" chapter). NSC's
// raw CIM export deviates from CGMES in two structural ways that were
// found to be pure format quirks — not genuine modeling differences — and
// are therefore normalized entirely at this import layer, keeping
// internal/core and internal/impl (and the generic Terminal-resolution
// logic in internal/impl/common) completely dialect-agnostic:
//
//  1. Terminal.ACDCTerminal.sequenceNumber is 0-based (0/1) instead of
//     CGMES's 1-based convention (1/2) for two-terminal Equipment (and 0
//     instead of 1 for single-terminal source/sink Equipment). Rewritten
//     here: 0->1, 1->2.
//  2. A physical busbar is modeled as ONE BusbarSection Equipment object
//     with N Terminals (one per connected Feeder/Bay), instead of CGMES's
//     convention of one Terminal per BusbarSection plus separate Feeder
//     Equipment. This breaks the "1 or 2 Terminals per Equipment"
//     invariant, so it is split here into N separate single-Terminal
//     BusbarSection objects (one keeps the original ID, the rest get
//     synthetic "<originalID>#<n>" IDs), each Terminal reassigned to its
//     own copy. Every copy carries a full duplicate of the original
//     BusbarSection's own attributes (Name, EquipmentContainer, ...), so
//     BuildAttributes still sees the complete attribute set on each, and
//     BuildContainers still groups all copies into the same busbar
//     Container (they share the same VoltageLevel via
//     Equipment.EquipmentContainer). Any real electrical connection this
//     hides (the copies now look like disconnected components) is
//     recovered by internal/impl/common's MergeBusbarSectionNodes, which
//     already exists to handle exactly this "same busbar Container, no
//     explicit connecting Equipment" case.
//
// A BusbarSection's own Terminals are numbered as a plain 1-based
// enumeration of "which Feeder is this" (1, 2, 3, ...) — a different
// semantic from the 0/1 two-terminal-role numbering fix 1 corrects for
// ordinary Zweipol/single-terminal-source Equipment. Every BusbarSection
// Terminal's sequenceNumber is therefore unconditionally forced to "1"
// here (every BusbarSection copy has exactly one Terminal after the split
// above), regardless of the raw enumeration value.
//
// Both fixes need cross-record context (which Terminals reference which
// BusbarSection; how many Terminals a given BusbarSection has). Rather
// than buffering a whole file's StagingRecords in memory to get that
// context (which does not scale — confirmed by an actual ~1GB NSC load
// test file ballooning to several GB of RAM), StreamFile makes two
// bounded-memory passes over the same file using the same streaming
// cgmes.ParseFileSAXStream parser used for CGMES:
//
//   - Pass 1 builds two small maps: Terminal ID -> its
//     Terminal.ConductingEquipment value, and the set of BusbarSection
//     IDs. From these it derives, per Terminal, whether it belongs to a
//     BusbarSection and (if that BusbarSection has more than one
//     Terminal) which ID it should end up pointing at. Peak memory here
//     is proportional to the number of distinct Terminal and
//     BusbarSection objects in the file, not to the file's total
//     attribute payload.
//   - Pass 2 re-streams the file, rewriting/duplicating records on the
//     fly using the maps from pass 1, and calls emit immediately for each
//     — nothing from pass 2 is buffered at all.
//
// This trades one extra sequential file read for a hard bound on memory,
// consistent with Konzept.md's resource-usage mandate (reads/imports must
// not scale linearly with model size).
package nsc

import (
	"errors"
	"fmt"
	"sort"

	"gitlab.com/openk-nsc/jag/internal/importer/cgmes"
	"gitlab.com/openk-nsc/jag/internal/importer/model"
)

// DuplicateIDError is returned by StreamFile when an object ID appears
// both in the current file and in a file previously passed to the same
// idSourceFile map (see StreamFile's doc comment and
// phase1.RunNSCFiles: NSC files are independent standalone scenarios with
// no model-metadata header to say whether two files are ever meant to
// share an object ID, so this is treated as fatal rather than silently
// merged or overwritten).
type DuplicateIDError struct {
	ID         string
	FirstFile  string
	SecondFile string
}

func (e *DuplicateIDError) Error() string {
	return fmt.Sprintf("nsc: duplicate object ID %q found in both %s and %s — NSC files are independent scenarios and must not share object IDs", e.ID, e.FirstFile, e.SecondFile)
}

// StreamFile parses path (an NSC-dialect RDF/XML file) and calls emit for
// every resulting StagingRecord, with both NSC-dialect fixes from the
// package doc applied, without ever buffering the whole file's records in
// memory (see package doc for the two-pass design).
//
// idSourceFile tracks, across possibly multiple calls to StreamFile for
// different files of the same import run, which file each object ID was
// first seen in; passing the same map across calls turns on the
// cross-file duplicate-ID check (see DuplicateIDError). Pass a fresh empty
// map, or nil to disable the check entirely (e.g. for a single-file call
// where cross-file duplicates cannot occur).
func StreamFile(path string, idSourceFile map[string]string, emit func(model.StagingRecord) error) error {
	// Pass 1: index Terminal -> ConductingEquipment, each Terminal's own
	// raw sequenceNumber, and the set of BusbarSection IDs; also perform
	// the cross-file duplicate-ID check inline, so this remains a single
	// sequential pass over the file rather than a third one.
	termCondEq := map[string]string{}
	termSeq := map[string]string{}
	busbarSeen := map[string]bool{}

	err := cgmes.ParseFileSAXStream(path, "", func(r model.StagingRecord) error {
		if idSourceFile != nil {
			if firstFile, seen := idSourceFile[r.ID]; seen {
				if firstFile != path {
					return &DuplicateIDError{ID: r.ID, FirstFile: firstFile, SecondFile: path}
				}
			} else {
				idSourceFile[r.ID] = path
			}
		}
		switch {
		case r.Class == "Terminal" && r.Attribute == "Terminal.ConductingEquipment":
			termCondEq[r.ID] = r.Value
		case r.Class == "Terminal" && r.Attribute == "ACDCTerminal.sequenceNumber":
			termSeq[r.ID] = r.Value
		case r.Class == "BusbarSection":
			busbarSeen[r.ID] = true
		}
		return nil
	})
	if err != nil {
		var dupErr *DuplicateIDError
		if errors.As(err, &dupErr) {
			return dupErr
		}
		return err
	}

	terminalsByBusbar := map[string][]string{}
	eqTerminals := map[string][]string{} // ConductingEquipment ID -> its non-BusbarSection Terminal IDs
	for tID, condEq := range termCondEq {
		if busbarSeen[condEq] {
			terminalsByBusbar[condEq] = append(terminalsByBusbar[condEq], tID)
		} else {
			eqTerminals[condEq] = append(eqTerminals[condEq], tID)
		}
	}

	assignedCondEq := map[string]string{} // Terminal ID -> ConductingEquipment value to use
	busbarTerminal := map[string]bool{}   // Terminal ID -> is a busbar Terminal (force sequenceNumber "1")
	syntheticIDs := map[string][]string{} // original BusbarSection ID -> synthetic IDs needing a duplicate attribute set

	var busbarIDs []string
	for id := range terminalsByBusbar {
		busbarIDs = append(busbarIDs, id)
	}
	sort.Strings(busbarIDs)
	for _, busbarID := range busbarIDs {
		terms := append([]string(nil), terminalsByBusbar[busbarID]...)
		sort.Strings(terms)
		for _, t := range terms {
			busbarTerminal[t] = true
		}
		// terms[0] keeps the original busbarID unchanged; the rest get a
		// synthetic copy.
		assignedCondEq[terms[0]] = busbarID
		for i, t := range terms[1:] {
			synthID := fmt.Sprintf("%s#%d", busbarID, i+2)
			assignedCondEq[t] = synthID
			syntheticIDs[busbarID] = append(syntheticIDs[busbarID], synthID)
		}
	}

	// assignedSeq decides, per ordinary (non-BusbarSection) Terminal,
	// whether its raw sequenceNumber needs the 0-based->1-based fix — see
	// package doc fix 1. This is decided per owning Equipment from the
	// ACTUAL raw pair of values found on its Terminals, not by a blind
	// global "0->1, 1->2" substitution: a real ~1GB NSC load-test file
	// surfaced Equipment that already uses the correct 1-based values
	// (e.g. a two-terminal Fuse with raw {"1","2"}), which a blind global
	// remap would have corrupted into a duplicate ("1"->"2" collides with
	// the existing "2"). Only Equipment whose raw Terminal pair is
	// unambiguously the 0-based scheme ({"0"} alone for a single-terminal
	// Equipment, {"0","1"} for a two-terminal Equipment) gets rewritten;
	// anything else (already-correct 1-based values, or a genuinely
	// malformed pair) is left untouched and surfaces as a Phase 2 Anomaly
	// downstream instead of being silently forced into looking valid.
	assignedSeq := map[string]string{}
	for _, terms := range eqTerminals {
		switch len(terms) {
		case 1:
			if termSeq[terms[0]] == "0" {
				assignedSeq[terms[0]] = "1"
			}
		case 2:
			sortedTerms := append([]string(nil), terms...)
			sort.Strings(sortedTerms)
			a, b := sortedTerms[0], sortedTerms[1]
			va, vb := termSeq[a], termSeq[b]
			if (va == "0" && vb == "1") || (va == "1" && vb == "0") {
				if va == "0" {
					assignedSeq[a], assignedSeq[b] = "1", "2"
				} else {
					assignedSeq[a], assignedSeq[b] = "2", "1"
				}
			}
			// Any other raw pair (already 1/2, or a genuine anomaly like
			// a duplicate) is left untouched.
		default:
			// >2 raw Terminals on one Equipment can't be reliably
			// classified here — left untouched, surfaces downstream as
			// its own Phase 2 Anomaly (as already happens today).
		}
	}

	// Pass 2: re-stream the file, transforming and emitting immediately —
	// nothing here is buffered.
	return cgmes.ParseFileSAXStream(path, "", func(r model.StagingRecord) error {
		switch {
		case r.Class == "Terminal" && r.Attribute == "ACDCTerminal.sequenceNumber":
			if busbarTerminal[r.ID] {
				r.Value = "1"
			} else if newVal, ok := assignedSeq[r.ID]; ok {
				r.Value = newVal
			}
		case r.Class == "Terminal" && r.Attribute == "Terminal.ConductingEquipment":
			if newID, ok := assignedCondEq[r.ID]; ok {
				r.Value = newID
			}
		}
		if err := emit(r); err != nil {
			return err
		}
		if r.Class == "BusbarSection" {
			for _, synthID := range syntheticIDs[r.ID] {
				dup := r
				dup.ID = synthID
				if err := emit(dup); err != nil {
					return err
				}
			}
		}
		return nil
	})
}
