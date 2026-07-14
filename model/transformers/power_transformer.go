package transformers

import (
	"gitlab.com/openk-nsc/jag/model/common"
	"gitlab.com/openk-nsc/jag/model/limits"
)

// PowerTransformer is a (2-winding) transformer -- modeled in JAG as a
// single ordinary Zweipol edge connecting its HV (OS) node directly to its
// LV (US) node, NOT as a four-terminal element with a virtual star-point
// node (see spec/Konzept.md, Transformer-Entscheidung). Multi-winding
// (>2 voltage level) transformers are explicitly unsupported by JAG (hard
// import failure). CIM: IEC61970 Base "PowerTransformer" (extends
// "ConductingEquipment", "EquipmentContainer").
type PowerTransformer struct {
	common.Equipment
	OperationalLimitSet                    *limits.OperationalLimitSet `json:"operationalLimitSet,omitempty"`                    // optional; CIM: Equipment.OperationalLimitSet -- keine Einheit
	Ends                                   []*PowerTransformerEnd      `json:"ends,omitempty"`                                   // optional; CIM: PowerTransformer.PowerTransformerEnd -- keine Einheit; die (üblicherweise 2) Wicklungsenden
	IsPartOfGeneratorUnit                  *bool                       `json:"isPartOfGeneratorUnit,omitempty"`                  // optional; CIM: PowerTransformer.isPartOfGeneratorUnit -- keine Einheit
	OperationalValuesConsidered            *bool                       `json:"operationalValuesConsidered,omitempty"`            // optional; CIM: PowerTransformer.operationalValuesConsidered -- keine Einheit
	BeforeShCircuitHighestOperatingCurrent *float64                    `json:"beforeShCircuitHighestOperatingCurrent,omitempty"` // optional; CIM: PowerTransformer.beforeShCircuitHighestOperatingCurrent -- Einheit: Ampere (A)
	BeforeShCircuitHighestOperatingVoltage *float64                    `json:"beforeShCircuitHighestOperatingVoltage,omitempty"` // optional; CIM: PowerTransformer.beforeShCircuitHighestOperatingVoltage -- Einheit: kV
	BeforeShortCircuitAnglePf              *float64                    `json:"beforeShortCircuitAnglePf,omitempty"`              // optional; CIM: PowerTransformer.beforeShortCircuitAnglePf -- Einheit: Grad
	HighSideMinOperatingU                  *float64                    `json:"highSideMinOperatingU,omitempty"`                  // optional; CIM: PowerTransformer.highSideMinOperatingU -- Einheit: kV
}

func (t *PowerTransformer) IsEquipmentContainer() {}

var _ common.EquipmentContainer = (*PowerTransformer)(nil)
