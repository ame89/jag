package connectionusers

import (
	"gitlab.com/openk-nsc/jag/model/common"
	"gitlab.com/openk-nsc/jag/model/metadata"
)

// EnergyConsumer is a generic load (Auspeiser/Consumer role) drawing
// energy from the grid at a connection point. CIM: IEC61970 Base
// "EnergyConsumer" (extends "ConductingEquipment").
type EnergyConsumer struct {
	common.Equipment
	BaseVoltage     *metadata.BaseVoltage `json:"baseVoltage,omitempty"`     // optional; CIM: ConductingEquipment.BaseVoltage -- keine Einheit
	P               *float64              `json:"p,omitempty"`               // optional; CIM: EnergyConsumer.p -- Einheit: MW
	Q               *float64              `json:"q,omitempty"`               // optional; CIM: EnergyConsumer.q -- Einheit: MVAr
	PhaseConnection *string               `json:"phaseConnection,omitempty"` // optional; CIM: EnergyConsumer.phaseConnection -- keine Einheit
}
