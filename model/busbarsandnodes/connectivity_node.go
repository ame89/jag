package busbarsandnodes

import (
	"gitlab.com/openk-nsc/jag/model/common"
)

// ConnectivityNode is the physical connection point shared by the
// Terminals of the equipment attached to it, before any zero-ohm/switch
// reduction is applied. CIM: IEC61970 Base "ConnectivityNode".
type ConnectivityNode struct {
	common.IdentifiedObject
	ConnectivityNodeContainer common.ConnectivityNodeContainer `json:"connectivityNodeContainer,omitempty"` // optional; CIM: ConnectivityNode.ConnectivityNodeContainer -- keine Einheit
	TopologicalNode           *TopologicalNode                 `json:"topologicalNode,omitempty"`           // optional; CIM: ConnectivityNode.TopologicalNode -- keine Einheit; Zuordnung nach Nullohm-Reduktion, siehe spec/Konzept.md
}
