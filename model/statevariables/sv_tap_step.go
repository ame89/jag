package statevariables

import (
	"gitlab.com/openk-nsc/jag/model/common"
)

// SvTapStep is a state-variable (calculation result) object holding the
// currently-computed tap position of a tap changer -- not a live
// measurement. No attributes beyond the base IdentifiedObject fields were
// verified against our example data -- this is a minimal placeholder.
// CIM: CGMES SV profile "SvTapStep".
type SvTapStep struct {
	common.IdentifiedObject
}
