package measurement

import (
	"gitlab.com/openk-nsc/jag/model/common"
)

// Analog is a measurement point definition (metadata describing what is
// measured, e.g. "active power at Terminal X") -- not the value itself
// (see AnalogValue). No attributes beyond the base fields were verified
// against our example data -- this is a minimal placeholder.
// CIM: IEC61970 Meas "Analog" (extends "Measurement").
type Analog struct {
	common.IdentifiedObject
}
