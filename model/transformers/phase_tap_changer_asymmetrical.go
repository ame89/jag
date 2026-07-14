package transformers

import (
	"gitlab.com/openk-nsc/jag/model/common"
)

// PhaseTapChangerAsymmetrical models a phase-shifting tap changer with an
// asymmetrical winding connection. No attributes beyond the base
// IdentifiedObject fields were verified against our example data -- this
// is a minimal placeholder. CIM: IEC61970 Base
// "PhaseTapChangerAsymmetrical" (extends "PhaseTapChangerNonLinear").
type PhaseTapChangerAsymmetrical struct {
	common.IdentifiedObject
}
