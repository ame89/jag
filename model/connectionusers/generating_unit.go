package connectionusers

import (
	"gitlab.com/openk-nsc/jag/model/common"
)

// GeneratingUnit is the non-electrical "plant" side of a generator
// (fuel/prime-mover aggregate), referenced by SynchronousMachine.
// CIM: IEC61970 Base "GeneratingUnit" (extends "Equipment").
type GeneratingUnit struct {
	common.Equipment
	MaxOperatingP *float64 `json:"maxOperatingP,omitempty"` // optional; CIM: GeneratingUnit.maxOperatingP -- Einheit: MW
	MinOperatingP *float64 `json:"minOperatingP,omitempty"` // optional; CIM: GeneratingUnit.minOperatingP -- Einheit: MW
	RatedNetMaxP  *float64 `json:"ratedNetMaxP,omitempty"`  // optional; CIM: GeneratingUnit.ratedNetMaxP -- Einheit: MW
}
