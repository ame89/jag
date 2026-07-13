// Package model contains the pure data structures of JAG's node-edge model:
// Container, Equipment, Node, Edge, Geometry, Attribute and CatalogEntry.
//
// Per Impl.md, /internal/core (and its subpackages) contains only interfaces
// (storage abstractions) and pure data structures — no logic at all. Business
// logic that builds on these types lives in /internal/impl, never here.
package model

// ContainerType names a container's kind (e.g. "substation", "bay",
// "busbar", "acline", "junction", "distribution-box"). The closed enum of
// allowed values and the path-template validation rules (see Konzept.md,
// "Container / Hierarchie") are deliberately NOT defined here — core has no
// domain knowledge, not even in the form of which named constants exist.
// They live in /internal/impl (business logic depending on core), so the
// core stays a generic, domain-agnostic container tree; a semantic layer
// above /internal/impl can later attach concrete meaning (e.g. typed
// Ortsnetzstation/KVS/Trafo structs) without the core ever needing to know
// about it.
type ContainerType string

// Container is a node in the strict container tree (exactly one immediate
// parent, no multi-parenting). ParentID is empty for top-level containers
// (e.g. Substation, ACLine, Junction). Name/Label are deliberately not
// struct fields here — like any other descriptive data they flow through
// the generic Attribute (Sachdaten) mechanism instead, under reserved keys
// (see /internal/impl/common's AttributeKeyName/AttributeKeyLabel), so
// there is exactly one generic data channel for descriptive data rather
// than two parallel ones. Historisation was dropped entirely (see
// Konzept.md) — there is no Version field, Upsert always overwrites.
type Container struct {
	ID       string
	Type     ContainerType
	ParentID string // empty for top-level containers
}

// NodeKind distinguishes real (physically present) from virtual (technically
// needed but not physically present) nodes. See Konzept.md, "Knoten".
type NodeKind string

const (
	NodeKindReal    NodeKind = "real"
	NodeKindVirtual NodeKind = "virtual"
)

// Equipment is any electrical part/assembly (fuse, meter, coil, switch,
// transformer, etc.). Node and Edge are not standalone objects — they are
// composed roles of an Equipment (composition, not separate entities). Only
// Equipment itself has a ContainerID; Node/Edge reference their Equipment via
// EquipmentID and have no container membership of their own. Name/Label are
// deliberately not struct fields here — see Container's doc comment; they
// flow through Attribute (Sachdaten) instead. Historisation was dropped
// entirely (see Konzept.md) — there is no Version field.
type Equipment struct {
	ID          string
	ContainerID string
}

// Node is the "Node role" of an Equipment: an electrical connection point
// (e.g. a busbar section). An Equipment has, per current observation, either
// a Node role or an Edge role, never both — this is a Phase 3 validation
// rule, not a struct-level invariant.
type Node struct {
	EquipmentID string // reference to the owning Equipment
	Kind        NodeKind
}

// Edge is the "Edge role" of an Equipment: a Zweipol (two-terminal/one-port)
// element with exactly two connections to Nodes — deliberately not a
// Zweitor. Terminal1 points toward the higher voltage level or toward a
// transformer; Terminal2 points toward ground/earth potential (GND). At
// least one of these two rules must hold for any given Edge.
type Edge struct {
	EquipmentID     string // reference to the owning Equipment
	Terminal1NodeID string // toward higher voltage level / toward transformer
	Terminal2NodeID string // toward ground/earth potential (GND)
}

// GeometryOwnerKind distinguishes which kind of object a Geometry belongs
// to: Equipment (e.g. an ACLineSegment, a BusbarSection) or Container (e.g.
// a Substation, an ACLine container) — CIM's PowerSystemResource.Location
// attaches to both, and Konzept.md's decision that stations/lines/splices
// always get geometry means Container-owned Geometry has to be
// representable too, not just Equipment-owned.
type GeometryOwnerKind string

const (
	GeometryOwnerEquipment GeometryOwnerKind = "equipment"
	GeometryOwnerContainer GeometryOwnerKind = "container"
)

// Geometry is a 2D WGS 84 position (no height/depth) composed onto either
// an Equipment or a Container, 0..1 per owner. OwnerKind disambiguates
// which ID namespace OwnerID belongs to (Equipment IDs and Container IDs
// are each unique within their own namespace, but not necessarily unique
// across both).
type Geometry struct {
	OwnerID   string
	OwnerKind GeometryOwnerKind
	Lat, Lon  float64
}

// AttributeKey identifies a Sachdaten key. Keys come from one single global
// enum shared across all element types (not per-type); value typing and
// single-vs-multi-value are attached to the key/enum entry, not to
// individual values. The concrete enum members are not yet finalized (see
// Konzept.md, Sachdaten) — this type exists so callers have a distinct,
// non-plain-string type to key off of.
type AttributeKey string

// Attribute is one Sachdaten key-value entry (EAV), deliberately
// denormalized. Typing of Value is resolved via the AttributeKey's metadata,
// not via the struct itself — Value is intentionally `any`. Multi-value keys
// produce multiple Attribute rows sharing the same OwnerID+Key, rather than
// a slice-typed Value field.
type Attribute struct {
	OwnerID string // usually an EquipmentID
	Key     AttributeKey
	Value   any
}

// CatalogEntry is a ParameterCatalog entry. It reuses the Attribute
// mechanism/global enum for its own fields (with a documented,
// non-enforced per-kind schema mapping — see Konzept.md). Catalog entries
// are NOT versioned — historisation was dropped entirely; Upsert
// overwrites an existing entry directly.
type CatalogEntry struct {
	ID         string
	Attributes []Attribute
}
