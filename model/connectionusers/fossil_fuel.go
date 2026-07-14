package connectionusers

import (
	"gitlab.com/openk-nsc/jag/model/common"
)

// FossilFuel describes one fuel type used by a ThermalGeneratingUnit. No
// attributes beyond the base IdentifiedObject fields were verified against
// our example data -- this is a minimal placeholder. CIM: IEC61970 Base
// "FossilFuel".
type FossilFuel struct {
	common.IdentifiedObject
	ThermalGeneratingUnit *ThermalGeneratingUnit `json:"thermalGeneratingUnit,omitempty"` // optional; CIM: FossilFuel.ThermalGeneratingUnit -- keine Einheit
}
