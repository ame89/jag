package hierarchy

import (
	"gitlab.com/openk-nsc/jag/model/common"
)

// Feeder is the low-voltage synonym role for Bay (see spec/Idee.md
// terminology table: "Feld -> Bay (CIM) / Feeder (Niederspannungs-Synonym)").
// No attributes beyond the base IdentifiedObject fields were verified
// against our example data -- this is a minimal placeholder.
// CIM: IEC61970 Base "Feeder" (extends "EquipmentContainer").
type Feeder struct {
	common.IdentifiedObject
}

func (f *Feeder) IsEquipmentContainer() {}

var _ common.EquipmentContainer = (*Feeder)(nil)
