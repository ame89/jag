package switchgear

// Disconnector is an isolating switch (Trenner) not rated to interrupt
// load/fault current -- zero-ohm when closed, in JAG's electrical topology
// view. No attributes beyond the Switch base fields were verified against
// our example data. CIM: IEC61970 Base "Disconnector" (extends "Switch").
type Disconnector struct {
	Switch
}
