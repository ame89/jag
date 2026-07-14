package hierarchy

import (
	"gitlab.com/openk-nsc/jag/model/common"
)

// Substation is a station (umbrella term covering Substation proper,
// Umschaltwerk, Mittelspannungsschaltanlage, Ortsnetzstation in JAG's own
// terminology -- distinguished only via a Sachdaten key, not a separate
// container type, see spec/Konzept.md). Holds one or more VoltageLevels.
// CIM: IEC61970 Base "Substation" (extends "EquipmentContainer").
type Substation struct {
	common.IdentifiedObject
	Region        *SubGeographicalRegion `json:"region,omitempty"`        // optional; CIM: Substation.Region -- keine Einheit
	VoltageLevels []*VoltageLevel        `json:"voltageLevels,omitempty"` // optional; CIM: Substation.VoltageLevels -- keine Einheit
}

func (s *Substation) IsEquipmentContainer() {}

var _ common.EquipmentContainer = (*Substation)(nil)
