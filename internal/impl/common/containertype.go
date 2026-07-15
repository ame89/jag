package common

import coremodel "gitlab.com/openk-nsc/jag/internal/core/model"

// Container type enum — moved here from core/model per the "generic core"
// simplification: core/model.ContainerType is just a plain string type with
// no domain knowledge; the closed set of legal values and (eventually) the
// path-template validation rules (see Konzept.md, "Container / Hierarchie")
// are business logic and therefore live here, not in core. Umbrella term
// "substation" covers Substation, Umschaltwerk, Mittelspannungsschaltanlage
// and Ortsnetzstation, distinguished only via a Sachdaten key (e.g.
// station_kind), not separate container types. "house" (decided 2026-07-14)
// is JAG's name for CIM's Building — a standalone, top-level container (like
// substation/acline) representing a customer's house/premises,
// holding house-internal Equipment (Meter, Fuse, ...); its
// PowerSystemResource.Location satellite (Anhängsel) is picked up generically
// by BuildGeometry, no special handling needed there.
//
// NOTE: there is deliberately no ContainerTypeJunction. A standalone
// Junction/Muffe outside a station is just a Node — Container membership
// for it is bookkeeping only (real cross-cable/branch queries go through
// topology, not Container.ParentID) — so it simply joins whichever
// "acline" container its own ConnectivityNode(s) already belong to (see
// pass_b.go's resolveStandaloneJunctions / container.go's
// buildACLineChains). An earlier "dedicated Muffen-Container" auto-
// creation idea was tried and then superseded by this simpler approach
// (decided with the user 2026-07-15).
const (
	ContainerTypeSubstation      coremodel.ContainerType = "substation"
	ContainerTypeBay             coremodel.ContainerType = "bay"
	ContainerTypeBusbar          coremodel.ContainerType = "busbar"
	ContainerTypeACLine          coremodel.ContainerType = "acline"
	ContainerTypeDistributionBox coremodel.ContainerType = "distribution-box"
	ContainerTypeHouse           coremodel.ContainerType = "house"
)
