package lines

import (
	"gitlab.com/openk-nsc/jag/model/common"
)

// Junction is a cable joint/splice ("Muffe") connecting two ACLineSegments
// -- a JAG Zweipol edge in its own right. A plain 2-port Junction
// (Durchgangsmuffe) does not end a JAG ACLine; only a branching
// Junction (Abzweig-/T-Muffe, 3+ connections) is a real topological
// branch point (see spec/Konzept.md, ACLine-boundary decision). No
// attributes beyond the base Equipment fields were verified against our
// example data. CIM: IEC61970 Base "Junction" (extends
// "ConductingEquipment").
type Junction struct {
	common.Equipment
}
