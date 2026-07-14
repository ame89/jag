package hierarchy

import (
	"gitlab.com/openk-nsc/jag/model/common"
)

// Bay groups the equipment of one feeder/field ("Feld") within a
// VoltageLevel -- role, not type: Abgangsfeld (outgoing) vs.
// Einspeisefeld/incoming feeder (see spec/Idee.md terminology table).
// CIM: IEC61970 Base "Bay" (extends "EquipmentContainer",
// "ConnectivityNodeContainer").
type Bay struct {
	common.IdentifiedObject
	VoltageLevel *VoltageLevel `json:"voltageLevel,omitempty"` // optional; CIM: Bay.VoltageLevel -- keine Einheit
}

func (b *Bay) IsEquipmentContainer()        {}
func (b *Bay) IsConnectivityNodeContainer() {}

var (
	_ common.EquipmentContainer        = (*Bay)(nil)
	_ common.ConnectivityNodeContainer = (*Bay)(nil)
)
