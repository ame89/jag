package transformers

import (
	"gitlab.com/openk-nsc/jag/model/common"
)

// RatioTapChanger models a transformer end's tap changer for voltage
// magnitude regulation (Anhängsel of PowerTransformerEnd). No attributes
// beyond the control reference were verified against our example data --
// this is a minimal placeholder. CIM: IEC61970 Base "RatioTapChanger"
// (extends "TapChanger").
type RatioTapChanger struct {
	common.IdentifiedObject
	TapChangerControl *TapChangerControl `json:"tapChangerControl,omitempty"` // optional; CIM: TapChanger.TapChangerControl -- keine Einheit
}
