package limits

import (
	"gitlab.com/openk-nsc/jag/model/common"
)

// CurrentLimit is a thermal current limit (e.g. the permanent admissible
// transmission loading, "patl") belonging to an OperationalLimitSet.
// CIM: IEC61970 Base "CurrentLimit".
type CurrentLimit struct {
	common.IdentifiedObject
	OperationalLimitSet  *OperationalLimitSet  `json:"operationalLimitSet,omitempty"`  // optional; CIM: OperationalLimit.OperationalLimitSet -- keine Einheit
	OperationalLimitType *OperationalLimitType `json:"operationalLimitType,omitempty"` // optional; CIM: OperationalLimit.OperationalLimitType -- keine Einheit
	Value                *float64              `json:"value,omitempty"`                // optional; CIM: CurrentLimit.value -- Einheit: Ampere (A)
}
