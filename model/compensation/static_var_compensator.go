package compensation

import (
	"gitlab.com/openk-nsc/jag/model/common"
)

// StaticVarCompensator (SVC) is a power-electronic reactive-power
// compensation device. No attributes beyond the base Equipment fields were
// verified against our example data -- this is a minimal placeholder.
// CIM: IEC61970 Base "StaticVarCompensator" (extends
// "RegulatingCondEq").
type StaticVarCompensator struct {
	common.Equipment
}
