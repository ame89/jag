package switchgear

// Fuse is a fusible cutout (Sicherung) -- zero-ohm when intact/closed, in
// JAG's electrical topology view. Some dialects (NSC) carry the trip
// current as Fuse.nominalCurrent instead of (or in parallel to)
// Switch.ratedCurrent -- both were observed in the same NSC example data.
// CIM: IEC61970 Base "Fuse" (extends "Switch").
type Fuse struct {
	Switch
	NominalCurrent *float64 `json:"nominalCurrent,omitempty"` // optional; CIM: Fuse.nominalCurrent -- Einheit: Ampere (A); NSC-Dialekt-Attributname, parallel zu Switch.RatedCurrent
}
