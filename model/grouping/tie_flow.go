package grouping

import (
	"gitlab.com/openk-nsc/jag/model/busbarsandnodes"
	"gitlab.com/openk-nsc/jag/model/common"
)

// TieFlow is the association-class linking a ControlArea to a boundary
// Terminal whose power flow counts toward that area's net interchange.
// CIM: IEC61970 Base "TieFlow".
type TieFlow struct {
	common.IdentifiedObject
	ControlArea *ControlArea              `json:"controlArea,omitempty"` // optional; CIM: TieFlow.ControlArea -- keine Einheit
	Terminal    *busbarsandnodes.Terminal `json:"terminal,omitempty"`    // optional; CIM: TieFlow.Terminal -- keine Einheit
	Positive    *bool                     `json:"positive,omitempty"`    // optional; CIM: TieFlow.positiveFlowIn -- keine Einheit
}
