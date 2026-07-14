package compensation

// NonlinearShuntCompensatorPoint is one row (one section count -> b/g
// value) of a NonlinearShuntCompensator's lookup table.
// CIM: IEC61970 Base "NonlinearShuntCompensatorPoint".
type NonlinearShuntCompensatorPoint struct {
	SectionNumber *int     `json:"sectionNumber,omitempty"` // optional; CIM: NonlinearShuntCompensatorPoint.sectionNumber -- keine Einheit
	B             *float64 `json:"b,omitempty"`             // optional; CIM: NonlinearShuntCompensatorPoint.b -- Einheit: Siemens
	G             *float64 `json:"g,omitempty"`             // optional; CIM: NonlinearShuntCompensatorPoint.g -- Einheit: Siemens
}
