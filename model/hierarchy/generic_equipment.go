package hierarchy

import (
	"gitlab.com/openk-nsc/jag/model/common"
)

// GenericEquipment is a catch-all CIM equipment class used where no more
// specific class applies. No attributes beyond the base Equipment fields
// were verified against our example data -- this is a minimal placeholder.
// CIM: IEC61970 Base "GenericEquipment" (extends "ConductingEquipment").
type GenericEquipment struct {
	common.Equipment
}
