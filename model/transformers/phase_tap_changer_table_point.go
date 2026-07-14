package transformers

// PhaseTapChangerTablePoint is one row (one tap step) of a
// PhaseTapChangerTable. No attributes beyond the step number were verified
// against our example data -- this is a minimal placeholder.
// CIM: IEC61970 Base "PhaseTapChangerTablePoint" (catalog).
type PhaseTapChangerTablePoint struct {
	Step *int `json:"step,omitempty"` // optional; CIM: TapChangerTablePoint.step -- keine Einheit
}
