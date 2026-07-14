package measurement

import (
	"gitlab.com/openk-nsc/jag/model/common"
	"gitlab.com/openk-nsc/jag/model/control"
	"gitlab.com/openk-nsc/jag/model/hierarchy"
)

// Meter is a physical metering device (Messung) at a UsagePoint, possibly
// linked to a TimeSchedule for time-of-use tariffs (NSC dialect).
// CIM: IEC61968 Metering "Meter" (extends "EndDevice").
type Meter struct {
	common.IdentifiedObject
	UsagePoint   *hierarchy.UsagePoint `json:"usagePoint,omitempty"`   // optional; CIM: EndDevice.UsagePoints -- keine Einheit
	TimeSchedule *control.TimeSchedule `json:"timeSchedule,omitempty"` // optional; NSC-Dialekt -- keine Einheit
	SerialNumber *string               `json:"serialNumber,omitempty"` // optional; CIM: Asset.serialNumber (via EndDevice) -- keine Einheit
}
