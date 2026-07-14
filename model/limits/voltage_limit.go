package limits

import (
	"gitlab.com/openk-nsc/jag/model/common"
)

// VoltageLimit is an allowed voltage-magnitude limit belonging to an
// OperationalLimitSet. CIM: IEC61970 Base "VoltageLimit".
type VoltageLimit struct {
	common.IdentifiedObject
	OperationalLimitSet  *OperationalLimitSet  `json:"operationalLimitSet,omitempty"`  // optional; CIM: OperationalLimit.OperationalLimitSet -- keine Einheit
	OperationalLimitType *OperationalLimitType `json:"operationalLimitType,omitempty"` // optional; CIM: OperationalLimit.OperationalLimitType -- keine Einheit
	Value                *float64              `json:"value,omitempty"`                // optional; CIM: VoltageLimit.value -- Einheit: kV
}
