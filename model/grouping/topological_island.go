package grouping

import (
	"gitlab.com/openk-nsc/jag/model/busbarsandnodes"
	"gitlab.com/openk-nsc/jag/model/common"
)

// TopologicalIsland groups all TopologicalNodes that are part of the same
// energized, connected island in a power-flow solution (CGMES SV profile).
// No attributes beyond the node list were verified against our example
// data. CIM: CGMES SV profile "TopologicalIsland".
type TopologicalIsland struct {
	common.IdentifiedObject
	TopologicalNodes []*busbarsandnodes.TopologicalNode `json:"topologicalNodes,omitempty"` // optional; CIM: TopologicalIsland.TopologicalNodes -- keine Einheit
}
