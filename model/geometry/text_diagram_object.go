package geometry

// TextDiagramObject is a DiagramObject variant carrying free text (e.g. a
// label) instead of/in addition to an equipment symbol.
// CIM: IEC61970 Diagram Layout "TextDiagramObject" (extends
// "DiagramObject").
type TextDiagramObject struct {
	DiagramObject
	Text *string `json:"text,omitempty"` // optional; CIM: TextDiagramObject.text -- keine Einheit
}
