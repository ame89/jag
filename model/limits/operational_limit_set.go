package limits

import (
	"gitlab.com/openk-nsc/jag/model/common"
)

// OperationalLimitSet groups the operational limits (CurrentLimit,
// VoltageLimit, ...) that apply to one Equipment/Terminal.
// CIM: IEC61970 Base "OperationalLimitSet".
type OperationalLimitSet struct {
	common.IdentifiedObject
}
