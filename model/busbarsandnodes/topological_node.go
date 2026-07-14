package busbarsandnodes

import (
	"gitlab.com/openk-nsc/jag/model/common"
	"gitlab.com/openk-nsc/jag/model/metadata"
	"gitlab.com/openk-nsc/jag/model/statevariables"
)

// TopologicalNode is the electrical connection point AFTER zero-ohm/switch
// reduction (CGMES TP profile) -- several ConnectivityNode objects may
// collapse onto one TopologicalNode via closed Breaker/Disconnector/Fuse
// elements between them. JAG's own ReliCapGrid_Espheim extraction uses
// TopologicalNode objects directly as pandapower buses (see
// pandapower/README.md). CIM: IEC61970 Topology "TopologicalNode".
type TopologicalNode struct {
	common.IdentifiedObject
	BaseVoltage               *metadata.BaseVoltage            `json:"baseVoltage,omitempty"`               // optional; CIM: TopologicalNode.BaseVoltage -- keine Einheit
	ConnectivityNodeContainer common.ConnectivityNodeContainer `json:"connectivityNodeContainer,omitempty"` // optional; CIM: TopologicalNode.ConnectivityNodeContainer -- keine Einheit
	SvVoltage                 *statevariables.SvVoltage        `json:"svVoltage,omitempty"`                 // optional; CIM: TopologicalNode.SvVoltage -- keine Einheit; Lastfluss-Rechenergebnis, keine Live-Messung
	Terminals                 []*Terminal                      `json:"-"`                                   // back-reference list, excluded from JSON to avoid cycles (see Terminal.TopologicalNode)
}
