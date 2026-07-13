// Package model contains the pure data structures of JAG's node-edge model:
// Container, Equipment, Node, Edge, Geometry, Attribute and CatalogEntry.
//
// Per Impl.md, /internal/core (and its subpackages) contains only interfaces
// (storage abstractions) and pure data structures — no logic at all. Business
// logic that builds on these types lives in /internal/impl, never here.
package model

// ContainerType is the closed enum of allowed container types (see Konzept.md,
// "Container / Hierarchie"). Umbrella term "substation" covers Substation,
// Umschaltwerk, Mittelspannungsschaltanlage and Ortsnetzstation, distinguished
// only via a Sachdaten key (e.g. station_kind), not separate container types.
type ContainerType string

const (
	ContainerTypeSubstation      ContainerType = "substation"
	ContainerTypeBay             ContainerType = "bay"
	ContainerTypeBusbar          ContainerType = "busbar"
	ContainerTypeACLine          ContainerType = "acline"
	ContainerTypeJunction        ContainerType = "junction"
	ContainerTypeDistributionBox ContainerType = "distribution-box"
)

// Container is a node in the strict container tree (exactly one immediate
// parent, no multi-parenting). ParentID is empty for top-level containers
// (e.g. Substation, ACLine, Junction).
type Container struct {
	ID       string
	Name     string
	Label    string
	Version  uint // currently always 1; historisation was dropped entirely
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
// EquipmentID and have no container membership of their own.
type Equipment struct {
	ID          string
	Name        string
	Label       string
	Version     uint // currently always 1; historisation was dropped entirely
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
