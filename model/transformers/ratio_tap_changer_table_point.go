package transformers

// RatioTapChangerTablePoint is one row (one tap step) of a
// RatioTapChangerTable. No attributes beyond the step number were
// verified against our example data -- this is a minimal placeholder.
// CIM: IEC61970 Base "RatioTapChangerTablePoint" (catalog).
type RatioTapChangerTablePoint struct {
	Step *int `json:"step,omitempty"` // optional; CIM: TapChangerTablePoint.step -- keine Einheit
}
