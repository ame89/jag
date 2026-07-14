package transformers

import (
	"gitlab.com/openk-nsc/jag/model/common"
)

// PhaseTapChangerTable is a catalog entry: a lookup table of
// PhaseTapChangerTablePoint rows for a tabular phase tap changer. No
// attributes beyond the point list were verified against our example data
// -- this is a minimal placeholder. CIM: IEC61970 Base
// "PhaseTapChangerTable" (catalog).
type PhaseTapChangerTable struct {
	common.IdentifiedObject
	Points []*PhaseTapChangerTablePoint `json:"points,omitempty"` // optional; CIM: PhaseTapChangerTable.PhaseTapChangerTablePoint -- keine Einheit
}
