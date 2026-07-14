package control

import (
	"gitlab.com/openk-nsc/jag/model/common"
)

// EnergySchedulingType categorizes a scheduled energy resource (e.g. for a
// PowerElectronicsUnit's planned/steuerbare output). No attributes beyond
// the base IdentifiedObject fields were verified against our example data.
// CIM: IEC61970 Base/Scheduling "EnergySchedulingType" (catalog-like
// classification).
type EnergySchedulingType struct {
	common.IdentifiedObject
}
