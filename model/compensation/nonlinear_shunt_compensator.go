package compensation

import (
	"gitlab.com/openk-nsc/jag/model/common"
	"gitlab.com/openk-nsc/jag/model/metadata"
)

// NonlinearShuntCompensator is a shunt capacitor/reactor bank whose
// admittance per section is given by a NonlinearShuntCompensatorPoint
// lookup table instead of a constant value. CIM: IEC61970 Base
// "NonlinearShuntCompensator" (extends "ShuntCompensator").
type NonlinearShuntCompensator struct {
	common.Equipment
	BaseVoltage *metadata.BaseVoltage             `json:"baseVoltage,omitempty"` // optional; CIM: ConductingEquipment.BaseVoltage -- keine Einheit
	Points      []*NonlinearShuntCompensatorPoint `json:"points,omitempty"`      // optional; CIM: NonlinearShuntCompensator.NonlinearShuntCompensatorPoints -- keine Einheit
}
