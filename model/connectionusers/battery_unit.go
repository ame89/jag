package connectionusers

import (
	"gitlab.com/openk-nsc/jag/model/common"
	"gitlab.com/openk-nsc/jag/model/control"
)

// BatteryUnit is a battery storage unit behind a
// PowerElectronicsConnection (NSC dialect) -- a Prosumer role (can both
// absorb and inject power). CIM: IEC61970 Base "BatteryUnit" (extends
// "PowerElectronicsUnit").
type BatteryUnit struct {
	common.IdentifiedObject
	PowerElectronicsConnection *control.PowerElectronicsConnection `json:"powerElectronicsConnection,omitempty"` // optional; CIM: PowerElectronicsUnit.PowerElectronicsConnection -- keine Einheit
	RatedE                     *float64                            `json:"ratedE,omitempty"`                     // optional; CIM: BatteryUnit.ratedE -- Einheit: kWh
	StoredE                    *float64                            `json:"storedE,omitempty"`                    // optional; CIM: BatteryUnit.storedE -- Einheit: kWh
}
