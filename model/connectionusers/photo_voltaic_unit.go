package connectionusers

import (
	"gitlab.com/openk-nsc/jag/model/common"
	"gitlab.com/openk-nsc/jag/model/control"
)

// PhotoVoltaicUnit is a PV generation unit behind a
// PowerElectronicsConnection (NSC dialect) -- a pure Producer role.
// CIM: IEC61970 Base "PhotoVoltaicUnit" (extends "PowerElectronicsUnit").
type PhotoVoltaicUnit struct {
	common.IdentifiedObject
	PowerElectronicsConnection *control.PowerElectronicsConnection `json:"powerElectronicsConnection,omitempty"` // optional; CIM: PowerElectronicsUnit.PowerElectronicsConnection -- keine Einheit
	MaxP                       *float64                            `json:"maxP,omitempty"`                       // optional; CIM: PowerElectronicsUnit.maxP -- Einheit: kW
	MinP                       *float64                            `json:"minP,omitempty"`                       // optional; CIM: PowerElectronicsUnit.minP -- Einheit: kW
}
