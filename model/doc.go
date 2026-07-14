// Package model contains plain Go structs mirroring the CIM element types
// documented in spec/Idee.md's "Fachliche Gruppierung der beobachteten
// CIM-Elementtypen" section. It exists to let Go code build/hold a CIM
// model in memory (e.g. for hand-written test fixtures or debugging tools),
// independent of JAG's own core node-edge model (internal/core, internal/impl).
//
// # Layout
//
// One subpackage per CIM element group from spec/Idee.md (switchgear,
// lines, transformers, busbarsandnodes, connectionusers, compensation,
// limits, control, hierarchy, geometry, statevariables, metadata,
// measurement, grouping), plus a shared "common" package with the base
// types every CIM class embeds (IdentifiedObject, Equipment) and the marker
// interfaces used for polymorphic references (EquipmentContainer,
// ConnectivityNodeContainer, ConductingEquipmentRef).
//
// One CIM class = one Go struct = one file (e.g. model/switchgear/breaker.go).
//
// # References are pointers, not IDs
//
// Where the raw CIM data uses an rdf:resource reference to another object,
// the corresponding Go struct field is a typed pointer (or, where CIM
// itself is polymorphic - e.g. Equipment.EquipmentContainer can be a
// Substation, VoltageLevel, Bay, Line, or Feeder - a small marker
// interface implemented by all of them) instead of a reference/ID string.
//
// # Avoiding JSON-marshal cycles
//
// Some CIM relationships are naturally bidirectional (e.g.
// PowerTransformer.PowerTransformerEnd forward-references its ends, while
// PowerTransformerEnd.PowerTransformer references back). Serializing both
// directions with encoding/json would recurse forever. Convention used
// throughout this package: the "child points to its parent/owner" direction
// (e.g. PowerTransformerEnd.PowerTransformer, Terminal.TopologicalNode) is a
// normal JSON field; the complementary "parent points down to its
// children/contents" direction (e.g. PowerTransformer.Ends,
// TopologicalNode.Terminals) is still a real Go field for in-memory
// navigation, but is tagged `json:"-"` and therefore excluded from
// serialization.
//
// # Optional attributes
//
// Almost every CIM attribute is optional in practice (frequently absent in
// real-world export files). To make this visible in Go, all scalar
// attributes beyond the base IdentifiedObject.MRID/.Name are modeled as
// pointer types (*string/*float64/*int/*bool) with a `json:",omitempty"`
// tag: a nil pointer means "attribute not set in the source data", as
// opposed to a present zero value. Struct/slice reference fields are
// pointers/slices already and are optional by nature (nil/empty = not set).
//
// # Provenance of the attribute lists
//
// Field lists come from spec/Idee.md's "Beobachtete CIM-Attribute je
// Elementtyp" / "Beobachtete Attribute in examples/nsc/" sections (i.e.
// attributes actually seen in the example datasets under examples/), not
// the full official CIM/CGMES class definition. Where spec/Idee.md
// documents no attributes for a class beyond the base IdentifiedObject
// fields, the struct's doc comment says so explicitly ("keine Attribute in
// den Beispieldaten belegt").
package model
