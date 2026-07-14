package lines

import (
	"gitlab.com/openk-nsc/jag/model/common"
)

// Line is a CIM EquipmentContainer grouping the ACLineSegments (and any
// intermediate Junction splices) of one cable/overhead-line route outside a
// station -- corresponds to JAG's own "acline" container type (see
// spec/Konzept.md). CIM: IEC61970 Base "Line" (extends
// "EquipmentContainer", "ConnectivityNodeContainer").
type Line struct {
	common.IdentifiedObject
}

func (l *Line) IsEquipmentContainer()        {}
func (l *Line) IsConnectivityNodeContainer() {}

var (
	_ common.EquipmentContainer        = (*Line)(nil)
	_ common.ConnectivityNodeContainer = (*Line)(nil)
)
