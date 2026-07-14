package geometry

import (
	"gitlab.com/openk-nsc/jag/model/common"
)

// DiagramObjectStyle describes the visual style (color, line style, ...) of
// one or more DiagramObject entries. No attributes beyond the base
// IdentifiedObject fields were verified against our example data.
// CIM: IEC61970 Diagram Layout "DiagramObjectStyle".
type DiagramObjectStyle struct {
	common.IdentifiedObject
}
