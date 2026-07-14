package connectionusers

import (
	"gitlab.com/openk-nsc/jag/model/common"
	"gitlab.com/openk-nsc/jag/model/metadata"
)

// SynchronousMachine is a rotating generator/motor (e.g. a conventional
// power-plant generator) connected to the grid. CIM: IEC61970 Base
// "SynchronousMachine" (extends "RotatingMachine").
type SynchronousMachine struct {
	common.Equipment
	BaseVoltage    *metadata.BaseVoltage `json:"baseVoltage,omitempty"`    // optional; CIM: ConductingEquipment.BaseVoltage -- keine Einheit
	RatedS         *float64              `json:"ratedS,omitempty"`         // optional; CIM: RotatingMachine.ratedS -- Einheit: MVA
	RatedU         *float64              `json:"ratedU,omitempty"`         // optional; CIM: RotatingMachine.ratedU -- Einheit: kV
	P              *float64              `json:"p,omitempty"`              // optional; CIM: RotatingMachine.p -- Einheit: MW
	Q              *float64              `json:"q,omitempty"`              // optional; CIM: RotatingMachine.q -- Einheit: MVAr
	GeneratingUnit *GeneratingUnit       `json:"generatingUnit,omitempty"` // optional; CIM: SynchronousMachine.GeneratingUnit -- keine Einheit
}
