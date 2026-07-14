package transformers

import (
	"gitlab.com/openk-nsc/jag/model/common"
)

// PhaseTapChangerLinear models a phase-shifting tap changer with a linear
// angle/step relationship. No attributes beyond the base IdentifiedObject
// fields were verified against our example data -- this is a minimal
// placeholder. CIM: IEC61970 Base "PhaseTapChangerLinear" (extends
// "PhaseTapChanger").
type PhaseTapChangerLinear struct {
	common.IdentifiedObject
}
