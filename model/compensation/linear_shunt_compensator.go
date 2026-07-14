package compensation

import (
	"gitlab.com/openk-nsc/jag/model/common"
	"gitlab.com/openk-nsc/jag/model/metadata"
)

// LinearShuntCompensator is a shunt capacitor/reactor bank whose
// admittance per section is constant (linear) -- a Zweipol edge to ground.
// CIM: IEC61970 Base "LinearShuntCompensator" (extends
// "ShuntCompensator").
type LinearShuntCompensator struct {
	common.Equipment
	BaseVoltage     *metadata.BaseVoltage `json:"baseVoltage,omitempty"`     // optional; CIM: ConductingEquipment.BaseVoltage -- keine Einheit
	BPerSection     *float64              `json:"bPerSection,omitempty"`     // optional; CIM: LinearShuntCompensator.bPerSection -- Einheit: Siemens
	GPerSection     *float64              `json:"gPerSection,omitempty"`     // optional; CIM: LinearShuntCompensator.gPerSection -- Einheit: Siemens
	MaximumSections *int                  `json:"maximumSections,omitempty"` // optional; CIM: ShuntCompensator.maximumSections -- keine Einheit
	NormalSections  *int                  `json:"normalSections,omitempty"`  // optional; CIM: ShuntCompensator.normalSections -- keine Einheit
}
