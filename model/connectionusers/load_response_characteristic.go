package connectionusers

import (
	"gitlab.com/openk-nsc/jag/model/common"
)

// LoadResponseCharacteristic describes how a load's active/reactive power
// varies with voltage (ZIP-model exponents), used for voltage-dependent
// load-flow calculation. CIM: IEC61970 Base "LoadResponseCharacteristic".
type LoadResponseCharacteristic struct {
	common.IdentifiedObject
	ExponentModel      *bool    `json:"exponentModel,omitempty"`      // optional; CIM: LoadResponseCharacteristic.exponentModel -- keine Einheit
	PVoltageExponent   *float64 `json:"pVoltageExponent,omitempty"`   // optional; CIM: LoadResponseCharacteristic.pVoltageExponent -- keine Einheit
	QVoltageExponent   *float64 `json:"qVoltageExponent,omitempty"`   // optional; CIM: LoadResponseCharacteristic.qVoltageExponent -- keine Einheit
	PConstantPower     *float64 `json:"pConstantPower,omitempty"`     // optional; CIM: LoadResponseCharacteristic.pConstantPower -- keine Einheit (Anteil 0..1)
	PConstantCurrent   *float64 `json:"pConstantCurrent,omitempty"`   // optional; CIM: LoadResponseCharacteristic.pConstantCurrent -- keine Einheit (Anteil 0..1)
	PConstantImpedance *float64 `json:"pConstantImpedance,omitempty"` // optional; CIM: LoadResponseCharacteristic.pConstantImpedance -- keine Einheit (Anteil 0..1)
}
