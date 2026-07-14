package hierarchy

import (
	"gitlab.com/openk-nsc/jag/model/common"
)

// GeographicalRegion is the top-level geographic grouping (e.g. a country
// or utility service area) -- a loose grouping, not part of JAG's own
// Container tree (see spec/Konzept.md's Netzregion decision, which notes
// GeographicalRegion/SubGeographicalRegion are likewise outside CIM's own
// ConnectivityNodeContainer tree). CIM: IEC61970 Base "GeographicalRegion".
type GeographicalRegion struct {
	common.IdentifiedObject
}
