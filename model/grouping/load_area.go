package grouping

import (
	"gitlab.com/openk-nsc/jag/model/common"
)

// LoadArea is a top-level geographic grouping of load-forecast areas. No
// attributes beyond the base IdentifiedObject fields were verified against
// our example data -- this is a minimal placeholder. CIM: IEC61970 Base
// "LoadArea".
type LoadArea struct {
	common.IdentifiedObject
}
