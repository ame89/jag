package grouping

import (
	"gitlab.com/openk-nsc/jag/model/common"
	"gitlab.com/openk-nsc/jag/model/connectionusers"
)

// ControlAreaGeneratingUnit is the association-class linking a
// ControlArea to one of its member GeneratingUnits. No attributes beyond
// the two references were verified against our example data.
// CIM: IEC61970 Base "ControlAreaGeneratingUnit".
type ControlAreaGeneratingUnit struct {
	common.IdentifiedObject
	ControlArea    *ControlArea                    `json:"controlArea,omitempty"`    // optional; CIM: ControlAreaGeneratingUnit.ControlArea -- keine Einheit
	GeneratingUnit *connectionusers.GeneratingUnit `json:"generatingUnit,omitempty"` // optional; CIM: ControlAreaGeneratingUnit.GeneratingUnit -- keine Einheit
}
