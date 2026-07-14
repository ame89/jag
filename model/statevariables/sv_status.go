package statevariables

import (
	"gitlab.com/openk-nsc/jag/model/common"
)

// SvStatus is a state-variable (calculation result) object holding the
// computed in-service status of an equipment -- not a live measurement. No
// attributes beyond the base IdentifiedObject fields were verified against
// our example data -- this is a minimal placeholder.
// CIM: CGMES SV profile "SvStatus".
type SvStatus struct {
	common.IdentifiedObject
}
