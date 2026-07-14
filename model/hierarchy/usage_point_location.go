package hierarchy

import (
	"gitlab.com/openk-nsc/jag/model/common"
	"gitlab.com/openk-nsc/jag/model/geometry"
)

// UsagePointLocation is the location of a house-connection/usage point
// (NSC dialect). CIM: IEC61968 Metering "UsagePointLocation" (extends
// "Location").
type UsagePointLocation struct {
	common.IdentifiedObject
	MainAddress *string                   `json:"mainAddress,omitempty"` // optional; CIM: UsagePointLocation.mainAddress -- keine Einheit
	Positions   []*geometry.PositionPoint `json:"positions,omitempty"`   // optional; CIM: Location.PositionPoints -- keine Einheit
}
