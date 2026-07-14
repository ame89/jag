package hierarchy

import (
	"gitlab.com/openk-nsc/jag/model/common"
)

// SubGeographicalRegion is a sub-division of a GeographicalRegion (e.g. a
// utility's regional service area), the direct parent of Substation in
// CIM's own hierarchy. CIM: IEC61970 Base "SubGeographicalRegion".
type SubGeographicalRegion struct {
	common.IdentifiedObject
	Region *GeographicalRegion `json:"region,omitempty"` // optional; CIM: SubGeographicalRegion.Region -- keine Einheit
}
