// Package common contains shared data structures and logic used by more than
// one /internal/impl subpackage (see Impl.md). This file holds the first
// building block of Phase 2 ("Referenzauflösung", see Konzept.md): turning
// the flat EAV StagingRecord rows produced by Phase 1 into an in-memory
// object graph that can be queried by ID/attribute, with references kept
// distinct from literal values.
package common

import "gitlab.com/openk-nsc/jag/internal/importer/model"

// AttrValue is one resolved attribute value: either a literal (IsReference
// false) or the ID of another object this attribute points to (IsReference
// true). Multi-valued attributes (e.g. repeated Terminal-like associations)
// keep all values in order; Seq from the StagingRecord is preserved so
// ordering (e.g. sequence numbers encoded as separate records) is not lost.
type AttrValue struct {
	Value       string
	IsReference bool
	Seq         int
}

// ObjectIndex is a read-only, in-memory view over the StagingRecords of one
// import version, grouped by object ID. It is deliberately dialect-neutral:
// it does not know about CIM/CGMES/NSC specifics, only about the generic
// (ID, Class, Attribute, Value, IsReference) shape Phase 1 already produces.
//
// Building this index is the first step of Phase 2's reference resolution
// (see Konzept.md): later steps (Terminal resolution, ConnectivityNode →
// Node mapping, Container derivation) are all built on top of it, not on the
// raw StagingRecord slice directly.
type ObjectIndex struct {
	classOf map[string]string
	attrs   map[string]map[string][]AttrValue // id -> attribute -> values
}

// BuildObjectIndex aggregates all given records into an ObjectIndex. Records
// for the same ID are expected to agree on Class; if they don't (which can
// legitimately happen for the generic placeholder "Equipment" class used by
// some profiles before the EQ profile's more specific class is known), the
// most specific (non-"Equipment") class wins, consistent with how the Phase
// 1 prototype (cmd/prototype/index.go) already resolves this.
func BuildObjectIndex(records []model.StagingRecord) *ObjectIndex {
	idx := &ObjectIndex{
		classOf: map[string]string{},
		attrs:   map[string]map[string][]AttrValue{},
	}
	for _, r := range records {
		if r.ID == "" {
			continue
		}
		if known, exists := idx.classOf[r.ID]; !exists || (known == "Equipment" && r.Class != "Equipment") {
			idx.classOf[r.ID] = r.Class
		}
		if idx.attrs[r.ID] == nil {
			idx.attrs[r.ID] = map[string][]AttrValue{}
		}
		idx.attrs[r.ID][r.Attribute] = append(idx.attrs[r.ID][r.Attribute], AttrValue{
			Value:       r.Value,
			IsReference: r.IsReference,
			Seq:         r.Seq,
		})
	}
	return idx
}

// ClassOf returns the resolved CIM class for id, or "" if id is unknown.
func (idx *ObjectIndex) ClassOf(id string) string {
	return idx.classOf[id]
}

// Exists reports whether id was seen at all.
func (idx *ObjectIndex) Exists(id string) bool {
	_, ok := idx.classOf[id]
	return ok
}

// HasAttr reports whether id has at least one value (literal or reference)
// for the given attribute.
func (idx *ObjectIndex) HasAttr(id, attribute string) bool {
	return len(idx.attrs[id][attribute]) > 0
}

// Attr returns the first literal value for the given attribute on id, or ""
// if none exists (either the attribute is absent, or all values for it are
// references, not literals).
func (idx *ObjectIndex) Attr(id, attribute string) string {
	for _, v := range idx.attrs[id][attribute] {
		if !v.IsReference {
			return v.Value
		}
	}
	return ""
}

// Ref returns the first reference target ID for the given attribute on id,
// or "" if none exists.
func (idx *ObjectIndex) Ref(id, attribute string) string {
	for _, v := range idx.attrs[id][attribute] {
		if v.IsReference {
			return v.Value
		}
	}
	return ""
}

// Refs returns all reference target IDs for the given attribute on id, in
// their original Seq order — used for multi-valued reference attributes.
func (idx *ObjectIndex) Refs(id, attribute string) []string {
	var out []string
	for _, v := range idx.attrs[id][attribute] {
		if v.IsReference {
			out = append(out, v.Value)
		}
	}
	return out
}

// NameOf returns IdentifiedObject.name for id, or "(unnamed)" if absent.
func (idx *ObjectIndex) NameOf(id string) string {
	if n := idx.Attr(id, "IdentifiedObject.name"); n != "" {
		return n
	}
	return "(unnamed)"
}

// AllAttrs returns every attribute recorded for id (both literal and
// reference values), keyed by attribute name. Used by callers that need to
// walk all of an object's own attributes generically (e.g. attaching a
// referenced satellite object's literal fields to some other owner).
func (idx *ObjectIndex) AllAttrs(id string) map[string][]AttrValue {
	return idx.attrs[id]
}

// IDsOfClass returns all object IDs whose resolved class equals class.
func (idx *ObjectIndex) IDsOfClass(class string) []string {
	var out []string
	for id, c := range idx.classOf {
		if c == class {
			out = append(out, id)
		}
	}
	return out
}
