package connectionusers

import (
	"gitlab.com/openk-nsc/jag/model/common"
)

// AsynchronousMachine is an induction generator/motor. No attributes
// beyond the base Equipment fields were verified against our example data
// -- this is a minimal placeholder. CIM: IEC61970 Base
// "AsynchronousMachine" (extends "RotatingMachine").
type AsynchronousMachine struct {
	common.Equipment
}
