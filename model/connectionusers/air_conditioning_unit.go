package connectionusers

import (
	"gitlab.com/openk-nsc/jag/model/common"
	"gitlab.com/openk-nsc/jag/model/control"
)

// AirConditioningUnit is a controllable consumption device (steuVA, §14a
// EnWG), analogous to Heatpump -- a Consumer role. No further attributes
// beyond the connection reference were verified against our example data.
// CIM: not a standard IEC61970 class -- NSC-dialect-specific equipment type.
type AirConditioningUnit struct {
	common.IdentifiedObject
	PowerElectronicsConnection *control.PowerElectronicsConnection `json:"powerElectronicsConnection,omitempty"` // optional; NSC-Dialekt -- keine Einheit
}
