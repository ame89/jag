package lines

import (
	"gitlab.com/openk-nsc/jag/model/common"
)

// PerLengthSequenceImpedance is a catalog entry describing per-kilometer
// impedance values for a cable/line type, referenced by ACLineSegment
// instead of (or in addition to) direct total r/x values -- the NSC-dialect
// pattern (see spec/Idee.md). CIM: IEC61970 Base
// "PerLengthSequenceImpedance" (catalog, extends "PerLengthImpedance").
type PerLengthSequenceImpedance struct {
	common.IdentifiedObject
	R  *float64 `json:"r,omitempty"`  // optional; CIM: PerLengthSequenceImpedance.r -- Einheit: Ohm/km (Mitsystem)
	R0 *float64 `json:"r0,omitempty"` // optional; CIM: PerLengthSequenceImpedance.r0 -- Einheit: Ohm/km (Nullsystem)
	X  *float64 `json:"x,omitempty"`  // optional; CIM: PerLengthSequenceImpedance.x -- Einheit: Ohm/km (Mitsystem)
	X0 *float64 `json:"x0,omitempty"` // optional; CIM: PerLengthSequenceImpedance.x0 -- Einheit: Ohm/km (Nullsystem)
}
