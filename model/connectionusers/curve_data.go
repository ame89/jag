package connectionusers

// CurveData is one (x, y1, y2) point of a ReactiveCapabilityCurve (or
// other CIM Curve). CIM: IEC61970 Base "CurveData".
type CurveData struct {
	Xvalue  *float64 `json:"xvalue,omitempty"`  // optional; CIM: CurveData.xvalue -- Einheit: MW (P)
	Y1value *float64 `json:"y1value,omitempty"` // optional; CIM: CurveData.y1value -- Einheit: MVAr (Qmin)
	Y2value *float64 `json:"y2value,omitempty"` // optional; CIM: CurveData.y2value -- Einheit: MVAr (Qmax)
}
