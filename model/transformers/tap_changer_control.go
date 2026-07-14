package transformers

import (
	"gitlab.com/openk-nsc/jag/model/common"
)

// TapChangerControl describes the regulation target (e.g. target voltage)
// of a RatioTapChanger/PhaseTapChanger. No attributes beyond the base
// IdentifiedObject fields were verified against our example data -- this
// is a minimal placeholder. CIM: IEC61970 Base "TapChangerControl"
// (extends "RegulatingControl").
type TapChangerControl struct {
	common.IdentifiedObject
}
