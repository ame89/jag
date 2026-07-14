package metadata

import (
	"gitlab.com/openk-nsc/jag/model/common"
)

// BaseVoltage is a catalog-like reference object describing one nominal
// voltage level (e.g. 20 kV), shared by all VoltageLevel/ConductingEquipment
// objects at that level. CIM: IEC61970 Base "BaseVoltage".
type BaseVoltage struct {
	common.IdentifiedObject
	NominalVoltage *float64 `json:"nominalVoltage,omitempty"` // optional; CIM: BaseVoltage.nominalVoltage -- Einheit: kV
}
