package geometry

// PositionPoint is one coordinate (x/y, optionally sequenced) of a
// Location's geometry (e.g. one vertex of a cable route).
// CIM: IEC61970 Base "PositionPoint".
type PositionPoint struct {
	Location       *Location `json:"-"`                        // back-reference to owning Location, excluded from JSON to avoid cycles (see Location.PositionPoints)
	SequenceNumber *int      `json:"sequenceNumber,omitempty"` // optional; CIM: PositionPoint.sequenceNumber -- keine Einheit
	XPosition      *float64  `json:"xPosition,omitempty"`      // optional; CIM: PositionPoint.xPosition -- Einheit je CoordinateSystem, i.d.R. Längengrad (WGS 84)
	YPosition      *float64  `json:"yPosition,omitempty"`      // optional; CIM: PositionPoint.yPosition -- Einheit je CoordinateSystem, i.d.R. Breitengrad (WGS 84)
}
