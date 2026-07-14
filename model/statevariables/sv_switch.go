package statevariables

import (
	"gitlab.com/openk-nsc/jag/model/common"
)

// SvSwitch is a state-variable (calculation result) object holding the
// computed switch position -- not a live measurement. No attributes beyond
// the base IdentifiedObject fields were verified against our example data
// -- this is a minimal placeholder. CIM: CGMES SV profile "SvSwitch".
type SvSwitch struct {
	common.IdentifiedObject
}
