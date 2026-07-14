package metadata

import (
	"gitlab.com/openk-nsc/jag/model/common"
)

// BoundaryPoint marks a CGMES model-boundary connection point (where one
// partial model's data ends and an external/boundary equivalent begins).
// No attributes beyond the base IdentifiedObject fields were verified
// against our example data -- this is a minimal placeholder.
// CIM: CGMES Boundary profile "BoundaryPoint".
type BoundaryPoint struct {
	common.IdentifiedObject
}
