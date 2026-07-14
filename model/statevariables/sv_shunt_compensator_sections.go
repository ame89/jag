package statevariables

import (
	"gitlab.com/openk-nsc/jag/model/common"
)

// SvShuntCompensatorSections is a state-variable (calculation result)
// object holding the currently-computed connected section count of a
// shunt compensator -- not a live measurement. No attributes beyond the
// base IdentifiedObject fields were verified against our example data --
// this is a minimal placeholder. CIM: CGMES SV profile
// "SvShuntCompensatorSections".
type SvShuntCompensatorSections struct {
	common.IdentifiedObject
}
