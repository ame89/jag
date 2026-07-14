package lines

import (
	"gitlab.com/openk-nsc/jag/model/common"
	"gitlab.com/openk-nsc/jag/model/limits"
	"gitlab.com/openk-nsc/jag/model/metadata"
)

// ACLineSegment is one cable/overhead-line segment -- a JAG Zweipol edge.
// Several ACLineSegments chained between two topological branch points form
// one logical JAG "ACLine" container (see spec/Konzept.md, ACLine-boundary
// decision). In CGMES, r/x/bch are given directly on the segment; in the
// NSC dialect they are instead looked up via PerLengthImpedance (see
// PerLengthSequenceImpedance). CIM: IEC61970 Base "ACLineSegment" (extends
// "Conductor").
type ACLineSegment struct {
	common.Equipment
	BaseVoltage                *metadata.BaseVoltage       `json:"baseVoltage,omitempty"`                // optional; CIM: ConductingEquipment.BaseVoltage -- keine Einheit
	OperationalLimitSet        *limits.OperationalLimitSet `json:"operationalLimitSet,omitempty"`        // optional; CIM: Equipment.OperationalLimitSet -- keine Einheit
	Length                     *float64                    `json:"length,omitempty"`                     // optional; CIM: Conductor.length -- Einheit: km
	R                          *float64                    `json:"r,omitempty"`                          // optional; CIM: ACLineSegment.r -- Einheit: Ohm (Mitsystem)
	X                          *float64                    `json:"x,omitempty"`                          // optional; CIM: ACLineSegment.x -- Einheit: Ohm (Mitsystem)
	R0                         *float64                    `json:"r0,omitempty"`                         // optional; CIM: ACLineSegment.r0 -- Einheit: Ohm (Nullsystem)
	X0                         *float64                    `json:"x0,omitempty"`                         // optional; CIM: ACLineSegment.x0 -- Einheit: Ohm (Nullsystem)
	Gch                        *float64                    `json:"gch,omitempty"`                        // optional; CIM: ACLineSegment.gch -- Einheit: Siemens (Mitsystem)
	Bch                        *float64                    `json:"bch,omitempty"`                        // optional; CIM: ACLineSegment.bch -- Einheit: Siemens (Mitsystem)
	G0ch                       *float64                    `json:"g0ch,omitempty"`                       // optional; CIM: ACLineSegment.g0ch -- Einheit: Siemens (Nullsystem)
	B0ch                       *float64                    `json:"b0ch,omitempty"`                       // optional; CIM: ACLineSegment.b0ch -- Einheit: Siemens (Nullsystem)
	ShortCircuitEndTemperature *float64                    `json:"shortCircuitEndTemperature,omitempty"` // optional; CIM: ACLineSegment.shortCircuitEndTemperature -- Einheit: °C
	PerLengthImpedance         *PerLengthSequenceImpedance `json:"perLengthImpedance,omitempty"`         // optional; CIM: ACLineSegment.PerLengthImpedance -- keine Einheit; NSC-Dialekt: Katalog-Nachschlagewert statt direkter r/x-Angabe oben
}
