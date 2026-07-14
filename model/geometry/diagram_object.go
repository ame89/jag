package geometry

import (
	"gitlab.com/openk-nsc/jag/model/common"
)

// DiagramObject positions one PowerSystemResource within a Diagram.
// CIM: IEC61970 Diagram Layout "DiagramObject".
type DiagramObject struct {
	common.IdentifiedObject
	Diagram             *Diagram              `json:"diagram,omitempty"`             // optional; CIM: DiagramObject.Diagram -- keine Einheit
	DiagramObjectStyle  *DiagramObjectStyle   `json:"diagramObjectStyle,omitempty"`  // optional; CIM: DiagramObject.DiagramObjectStyle -- keine Einheit
	DiagramObjectPoints []*DiagramObjectPoint `json:"diagramObjectPoints,omitempty"` // optional; CIM: DiagramObject.DiagramObjectPoints -- keine Einheit
}
