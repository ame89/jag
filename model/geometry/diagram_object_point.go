package geometry

// DiagramObjectPoint is one coordinate of a DiagramObject's on-screen
// drawing geometry (distinct from PositionPoint's real-world geometry).
// CIM: IEC61970 Diagram Layout "DiagramObjectPoint".
type DiagramObjectPoint struct {
	DiagramObject  *DiagramObject `json:"-"`                        // back-reference to owning DiagramObject, excluded from JSON to avoid cycles (see DiagramObject.DiagramObjectPoints)
	SequenceNumber *int           `json:"sequenceNumber,omitempty"` // optional; CIM: DiagramObjectPoint.sequenceNumber -- keine Einheit
	XPosition      *float64       `json:"xPosition,omitempty"`      // optional; CIM: DiagramObjectPoint.xPosition -- Einheit: Diagramm-/Bildschirmkoordinate (dimensionslos)
	YPosition      *float64       `json:"yPosition,omitempty"`      // optional; CIM: DiagramObjectPoint.yPosition -- Einheit: Diagramm-/Bildschirmkoordinate (dimensionslos)
}
