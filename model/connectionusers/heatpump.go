package connectionusers

import (
	"gitlab.com/openk-nsc/jag/model/common"
	"gitlab.com/openk-nsc/jag/model/control"
)

// Heatpump is a controllable consumption device (steuVA, §14a EnWG) behind
// a PowerElectronicsConnection/EnergyConsumer (NSC dialect) -- a Consumer
// role. No further attributes beyond the connection reference were
// verified against our example data. CIM: not a standard IEC61970 class --
// NSC-dialect-specific equipment type.
type Heatpump struct {
	common.IdentifiedObject
	PowerElectronicsConnection *control.PowerElectronicsConnection `json:"powerElectronicsConnection,omitempty"` // optional; NSC-Dialekt -- keine Einheit
}
