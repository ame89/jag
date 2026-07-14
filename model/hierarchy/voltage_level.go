package hierarchy

import (
	"gitlab.com/openk-nsc/jag/model/busbarsandnodes"
	"gitlab.com/openk-nsc/jag/model/common"
	"gitlab.com/openk-nsc/jag/model/metadata"
)

// VoltageLevel groups the equipment of a Substation operating at one
// voltage level, containing Bay(s) and/or a busbar structure.
// CIM: IEC61970 Base "VoltageLevel" (extends "EquipmentContainer",
// "ConnectivityNodeContainer").
type VoltageLevel struct {
	common.IdentifiedObject
	Substation       *Substation                        `json:"substation,omitempty"`       // optional; CIM: VoltageLevel.Substation -- keine Einheit
	BaseVoltage      *metadata.BaseVoltage              `json:"baseVoltage,omitempty"`      // optional; CIM: VoltageLevel.BaseVoltage -- keine Einheit
	HighVoltageLimit *float64                           `json:"highVoltageLimit,omitempty"` // optional; CIM: VoltageLevel.highVoltageLimit -- Einheit: kV
	LowVoltageLimit  *float64                           `json:"lowVoltageLimit,omitempty"`  // optional; CIM: VoltageLevel.lowVoltageLimit -- Einheit: kV
	Bays             []*Bay                             `json:"bays,omitempty"`             // optional; CIM: VoltageLevel.Bays -- keine Einheit
	TopologicalNodes []*busbarsandnodes.TopologicalNode `json:"-"`                          // back-reference list, excluded from JSON to avoid cycles (see TopologicalNode.ConnectivityNodeContainer)
}

func (v *VoltageLevel) IsEquipmentContainer()        {}
func (v *VoltageLevel) IsConnectivityNodeContainer() {}

var (
	_ common.EquipmentContainer        = (*VoltageLevel)(nil)
	_ common.ConnectivityNodeContainer = (*VoltageLevel)(nil)
)
