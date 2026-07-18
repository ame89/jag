// Package cim provides a curated, hand-maintained registry of CIM element
// (class) metadata — required/optional attributes, their data type and a
// German explanation of their meaning — used to generate commented,
// fill-in-the-blank HJSON scaffolds for JAG users (see cmd/hjsonscaffold).
//
// This replaces the earlier idea of deriving this metadata from the
// removed model/ package (a full per-CIM-class Go struct mirror, dropped
// 2026-07-15, commit 852f564 — "CIM struct generator no longer needed").
// Reviving model/ purely as a metadata source would undo that explicit
// decision, so instead the metadata lives as its own small, independent,
// hand-authored data set (see cimdata/*.hjson), populated from
// spec/Idee.md's "Fachliche Gruppierung der beobachteten CIM-Elementtypen"
// / "Beobachtete CIM-Attribute je Elementtyp" sections plus general CIM
// (IEC 61970/61968) domain knowledge, not regenerated from ./model.
//
// The data files are plain HJSON (not YAML/JSON) specifically so the
// metadata itself can carry inline comments — fitting, since hjson-go is
// already a project dependency for the (separate) HJSON Fachmodell import
// work, and avoids adding yet another new third-party format library only
// for this.
package cim

// TerminalKind classifies how many Terminals (physical connections) a CIM
// class' instances have — this drives whether/how a scaffold's "connects"
// list is pre-filled (see Konzept.md's HJSON netlist decision and Idee.md's
// Zweipol-Kennzeichnung).
type TerminalKind string

const (
	// TerminalsNone: no Terminal/connects at all — a pure container,
	// Anhängsel (satellite/sub-attribute), metadata, or catalog object
	// (Idee.md's ❌ categories).
	TerminalsNone TerminalKind = "0"
	// TerminalsOne: exactly one Terminal — a single-terminal source/sink
	// (e.g. EnergyConsumer, PowerElectronicsConnection); JAG wires
	// Terminal 2 to GND implicitly at Phase 2, so only one connects entry
	// is needed.
	TerminalsOne TerminalKind = "1"
	// TerminalsTwo: exactly two Terminals — an ordinary Zweipol/Edge
	// (Idee.md's ✅ classes).
	TerminalsTwo TerminalKind = "2"
	// TerminalsMany: a node-role class that may have more than two
	// Terminals, all denoting the SAME physical point (e.g.
	// BusbarSection with several feeder connections, or a branching
	// Junction/Abzweigmuffe) — see common.nodeRoleClasses.
	TerminalsMany TerminalKind = "n"
)

// Attribute describes one CIM attribute (or, for reference-typed
// attributes, one relationship) of a Class.
type Attribute struct {
	// Key is the CIM attribute name in "<Class>.<attribute>" form (e.g.
	// "Switch.normalOpen"), matching the staging/Sachdaten key convention
	// used elsewhere in this project (see internal/impl/common's doc
	// comments) — deliberately not just the bare attribute name, so a
	// generated scaffold's attribute keys are directly usable as HJSON
	// Fachmodell "attributes" map keys without renaming.
	Key string `json:"key"`
	// Type is a short, human-readable data type hint (e.g. "string",
	// "bool", "float", "int", "date", or a CIM reference like
	// "Referenz -> Substation").
	Type string `json:"type"`
	// Required marks a mandatory attribute (per CIM cardinality 1..1/1..n)
	// vs. an optional one (0..1/0..n). Not all CIM optionality is
	// consistently documented for every class in spec/Idee.md — this is a
	// pragmatic, best-effort classification, not a formally verified CIM
	// UML export.
	Required bool `json:"required"`
	// Description is a short German explanation of the attribute's
	// meaning/purpose, for the scaffold's inline comment.
	Description string `json:"description"`
}

// Class is one CIM element type's curated metadata entry.
type Class struct {
	// Name is filled in from the data file's map key, not read from the
	// file content itself (avoids the two ever disagreeing).
	Name string `json:"-"`
	// Description is a short German one-liner explaining what this CIM
	// class represents.
	Description string `json:"description"`
	// Group is the Idee.md "Fachliche Gruppierung" category this class
	// belongs to (e.g. "Schaltgeräte", "Transformatoren") — purely
	// informational, used for the scaffold header comment and the
	// "unknown class" listing in cmd/hjsonscaffold.
	Group string `json:"group"`
	// Terminals classifies the class' Terminal/connects shape (see
	// TerminalKind).
	Terminals TerminalKind `json:"terminals"`
	// Attributes is the curated attribute list, in the order they should
	// appear in a generated scaffold.
	Attributes []Attribute `json:"attributes"`
}

// classFile is the top-level shape of one cimdata/*.hjson file.
type classFile struct {
	Classes map[string]Class `json:"classes"`
}
