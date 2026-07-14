package switchgear

// Breaker is a circuit breaker (Lasttrennschalter) -- a Switch subtype
// capable of interrupting fault current. No attributes beyond the Switch
// base fields were verified against our example data.
// CIM: IEC61970 Base "Breaker" (extends "ProtectedSwitch"/"Switch").
type Breaker struct {
	Switch
}
