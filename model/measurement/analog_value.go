package measurement

import (
	"gitlab.com/openk-nsc/jag/model/common"
)

// AnalogValue holds one measured value for an Analog measurement point.
// Per spec/Idee.md, JAG does not currently ingest live measurement values
// -- this struct exists for lossless CIM import/export round-tripping
// only. CIM: IEC61970 Meas "AnalogValue" (extends "MeasurementValue").
type AnalogValue struct {
	common.IdentifiedObject
	Analog *Analog  `json:"analog,omitempty"` // optional; CIM: AnalogValue.Analog -- keine Einheit
	Value  *float64 `json:"value,omitempty"`  // optional; CIM: AnalogValue.value -- Einheit: abhängig vom gemessenen Analog (siehe Analog.unitSymbol)
}
