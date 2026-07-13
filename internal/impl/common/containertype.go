package common

import coremodel "gitlab.com/openk-nsc/jag/internal/core/model"

// Container type enum — moved here from core/model per the "generic core"
// simplification: core/model.ContainerType is just a plain string type with
// no domain knowledge; the closed set of legal values and (eventually) the
// path-template validation rules (see Konzept.md, "Container / Hierarchie")
// are business logic and therefore live here, not in core. Umbrella term
// "substation" covers Substation, Umschaltwerk, Mittelspannungsschaltanlage
// and Ortsnetzstation, distinguished only via a Sachdaten key (e.g.
// station_kind), not separate container types.
const (
	ContainerTypeSubstation      coremodel.ContainerType = "substation"
	ContainerTypeBay             coremodel.ContainerType = "bay"
	ContainerTypeBusbar          coremodel.ContainerType = "busbar"
	ContainerTypeACLine          coremodel.ContainerType = "acline"
	ContainerTypeJunction        coremodel.ContainerType = "junction"
	ContainerTypeDistributionBox coremodel.ContainerType = "distribution-box"
)
