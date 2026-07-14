package control

import (
	"gitlab.com/openk-nsc/jag/model/common"
)

// PSRType is a catalog entry classifying a PowerSystemResource (e.g. the
// steuVA/steuEA classification of a controllable connection point).
// CIM: IEC61970 Base "PSRType" (catalog).
type PSRType struct {
	common.IdentifiedObject
}
