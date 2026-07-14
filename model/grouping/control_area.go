package grouping

import (
	"gitlab.com/openk-nsc/jag/model/common"
)

// ControlArea is a balancing/scheduling area used for tie-flow and AGC
// accounting (e.g. one utility's control zone). CIM: IEC61970 Base
// "ControlArea".
type ControlArea struct {
	common.IdentifiedObject
	NetInterchange  *float64                     `json:"netInterchange,omitempty"`  // optional; CIM: ControlArea.netInterchange -- Einheit: MW
	GeneratingUnits []*ControlAreaGeneratingUnit `json:"generatingUnits,omitempty"` // optional; CIM: ControlArea.ControlAreaGeneratingUnit -- keine Einheit
	TieFlows        []*TieFlow                   `json:"tieFlows,omitempty"`        // optional; CIM: ControlArea.TieFlow -- keine Einheit
}
