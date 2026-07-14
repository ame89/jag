package transformers

import (
	"gitlab.com/openk-nsc/jag/model/common"
)

// PhaseTapChangerSymmetrical models a phase-shifting tap changer with a
// symmetrical winding connection. No attributes beyond the base
// IdentifiedObject fields were verified against our example data -- this
// is a minimal placeholder. CIM: IEC61970 Base "PhaseTapChangerSymmetrical"
// (extends "PhaseTapChangerNonLinear").
type PhaseTapChangerSymmetrical struct {
	common.IdentifiedObject
}
