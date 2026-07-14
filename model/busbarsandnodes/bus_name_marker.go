package busbarsandnodes

import (
	"gitlab.com/openk-nsc/jag/model/common"
)

// BusNameMarker groups Terminals that should share one displayed busbar
// name/label, independent of the actual ConnectivityNode/TopologicalNode
// topology. No attributes beyond the base IdentifiedObject fields were
// verified against our example data. CIM: IEC61970 Base "BusNameMarker".
type BusNameMarker struct {
	common.IdentifiedObject
}
