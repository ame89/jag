package transformers

import (
	"gitlab.com/openk-nsc/jag/model/common"
)

// RatioTapChangerTable is a catalog entry: a lookup table of
// RatioTapChangerTablePoint rows for a tabular ratio tap changer. No
// attributes beyond the point list were verified against our example data
// -- this is a minimal placeholder. CIM: IEC61970 Base
// "RatioTapChangerTable" (catalog).
type RatioTapChangerTable struct {
	common.IdentifiedObject
	Points []*RatioTapChangerTablePoint `json:"points,omitempty"` // optional; CIM: RatioTapChangerTable.RatioTapChangerTablePoint -- keine Einheit
}
