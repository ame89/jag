package control

import (
	"gitlab.com/openk-nsc/jag/model/common"
)

// RegulatingControl describes the steuVA/steuEA control rule attached to a
// PowerElectronicsConnection (e.g. §14a EnWG load control / §9 EEG feed-in
// management): whether it is a discrete or continuous control, whether it
// is enabled, and its allowed value range. CIM: IEC61970 Base
// "RegulatingControl".
type RegulatingControl struct {
	common.IdentifiedObject
	Discrete              *bool    `json:"discrete,omitempty"`              // optional; CIM: RegulatingControl.discrete -- keine Einheit
	Enabled               *bool    `json:"enabled,omitempty"`               // optional; CIM: RegulatingControl.enabled -- keine Einheit
	MinAllowedTargetValue *float64 `json:"minAllowedTargetValue,omitempty"` // optional; CIM: RegulatingControl.minAllowedTargetValue -- Einheit abhängig vom geregelten Wert (z.B. kV bei Spannungsregelung)
	MaxAllowedTargetValue *float64 `json:"maxAllowedTargetValue,omitempty"` // optional; CIM: RegulatingControl.maxAllowedTargetValue -- Einheit abhängig vom geregelten Wert (z.B. kV bei Spannungsregelung)
	TargetValue           *float64 `json:"targetValue,omitempty"`           // optional; CIM: RegulatingControl.targetValue -- Einheit abhängig vom geregelten Wert (z.B. p.u. Spannungssollwert)
	Mode                  *string  `json:"mode,omitempty"`                  // optional; CIM: RegulatingControl.mode -- keine Einheit (z.B. "voltage")
}
