package geometry

import (
	"gitlab.com/openk-nsc/jag/model/common"
)

// Location is a geographic/postal location attached to a
// PowerSystemResource (e.g. Substation, House/Building), holding either
// coordinates (via PositionPoint) or a postal address. JAG itself only
// stores 2D WGS-84 coordinates (see spec/Konzept.md, Geometrie-Kapitel),
// but the raw CIM Location can also carry a plain address.
// CIM: IEC61970 Base "Location".
type Location struct {
	common.IdentifiedObject
	CoordinateSystem *CoordinateSystem `json:"coordinateSystem,omitempty"` // optional; CIM: Location.CoordinateSystem -- keine Einheit
	MainAddress      *string           `json:"mainAddress,omitempty"`      // optional; CIM: Location.mainAddress -- keine Einheit
	PositionPoints   []*PositionPoint  `json:"positionPoints,omitempty"`   // optional; CIM: Location.PositionPoints -- keine Einheit
}
