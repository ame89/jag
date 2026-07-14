package transformers

import (
	"gitlab.com/openk-nsc/jag/model/common"
)

// PhaseTapChangerTabular models a phase-shifting tap changer whose
// angle/ratio per step is given by a lookup table
// (PhaseTapChangerTable/PhaseTapChangerTablePoint) rather than a formula.
// No attributes beyond the table reference were verified against our
// example data. CIM: IEC61970 Base "PhaseTapChangerTabular" (extends
// "PhaseTapChanger").
type PhaseTapChangerTabular struct {
	common.IdentifiedObject
	PhaseTapChangerTable *PhaseTapChangerTable `json:"phaseTapChangerTable,omitempty"` // optional; CIM: PhaseTapChangerTabular.PhaseTapChangerTable -- keine Einheit
}
