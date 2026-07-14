package hierarchy

import (
	"gitlab.com/openk-nsc/jag/model/common"
)

// UsagePoint is a house-connection / metering point (NSC dialect) --
// the point at which a Producer/Consumer/Prosumer connects to the grid.
// CIM: IEC61968 Metering "UsagePoint".
type UsagePoint struct {
	common.IdentifiedObject
	UsagePointLocation *UsagePointLocation `json:"usagePointLocation,omitempty"` // optional; CIM: UsagePoint.UsagePointLocation -- keine Einheit
	IsVirtual          *bool               `json:"isVirtual,omitempty"`          // optional; CIM: UsagePoint.isVirtual -- keine Einheit
	IsSdp              *bool               `json:"isSdp,omitempty"`              // optional; CIM: UsagePoint.isSdp -- keine Einheit (Supply Delivery Point)
	OutageRegion       *string             `json:"outageRegion,omitempty"`       // optional; CIM: UsagePoint.outageRegion -- keine Einheit
	PhaseCode          *string             `json:"phaseCode,omitempty"`          // optional; CIM: UsagePoint.phaseCode -- keine Einheit
	RatedPower         *float64            `json:"ratedPower,omitempty"`         // optional; CIM: UsagePoint.ratedPower -- Einheit: kW
}
