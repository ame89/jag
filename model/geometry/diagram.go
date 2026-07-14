package geometry

import (
	"gitlab.com/openk-nsc/jag/model/common"
)

// Diagram is a named diagram/drawing (e.g. a single-line diagram) that
// groups DiagramObject entries. No attributes beyond the base
// IdentifiedObject fields were verified against our example data.
// CIM: IEC61970 Diagram Layout "Diagram".
type Diagram struct {
	common.IdentifiedObject
}
