package transformers

import (
	"gitlab.com/openk-nsc/jag/model/busbarsandnodes"
	"gitlab.com/openk-nsc/jag/model/common"
	"gitlab.com/openk-nsc/jag/model/metadata"
)

// PowerTransformerEnd is one winding side (OS or US) of a PowerTransformer,
// carrying that side's rated values and short-circuit impedance. JAG maps
// TransformerEnd.endNumber (1/2) directly onto its own Terminal 1/2
// convention (1 = HV/OS side, 2 = LV/US side). Side-specific attributes are
// kept as two separate Sachdaten groups on the same JAG edge, not as
// separate virtual nodes (see spec/Konzept.md).
// CIM: IEC61970 Base "PowerTransformerEnd" (extends "TransformerEnd").
type PowerTransformerEnd struct {
	common.IdentifiedObject
	PowerTransformer       *PowerTransformer         `json:"-"`                                // back-reference to the owning transformer, excluded from JSON to avoid cycles (see PowerTransformer.Ends)
	EndNumber              *int                      `json:"endNumber,omitempty"`              // optional; CIM: TransformerEnd.endNumber -- keine Einheit; 1=OS/HV, 2=US/LV
	Terminal               *busbarsandnodes.Terminal `json:"terminal,omitempty"`               // optional; CIM: TransformerEnd.Terminal -- keine Einheit
	BaseVoltage            *metadata.BaseVoltage     `json:"baseVoltage,omitempty"`            // optional; CIM: TransformerEnd.BaseVoltage -- keine Einheit
	Grounded               *bool                     `json:"grounded,omitempty"`               // optional; CIM: TransformerEnd.grounded -- keine Einheit
	Rground                *float64                  `json:"rground,omitempty"`                // optional; CIM: TransformerEnd.rground -- Einheit: Ohm; nur relevant wenn Grounded=true
	Xground                *float64                  `json:"xground,omitempty"`                // optional; CIM: TransformerEnd.xground -- Einheit: Ohm; nur relevant wenn Grounded=true
	RatedU                 *float64                  `json:"ratedU,omitempty"`                 // optional; CIM: PowerTransformerEnd.ratedU -- Einheit: kV
	RatedS                 *float64                  `json:"ratedS,omitempty"`                 // optional; CIM: PowerTransformerEnd.ratedS -- Einheit: MVA
	R                      *float64                  `json:"r,omitempty"`                      // optional; CIM: PowerTransformerEnd.r -- Einheit: Ohm (Mitsystem)
	X                      *float64                  `json:"x,omitempty"`                      // optional; CIM: PowerTransformerEnd.x -- Einheit: Ohm (Mitsystem)
	R0                     *float64                  `json:"r0,omitempty"`                     // optional; CIM: PowerTransformerEnd.r0 -- Einheit: Ohm (Nullsystem)
	X0                     *float64                  `json:"x0,omitempty"`                     // optional; CIM: PowerTransformerEnd.x0 -- Einheit: Ohm (Nullsystem)
	G                      *float64                  `json:"g,omitempty"`                      // optional; CIM: PowerTransformerEnd.g -- Einheit: Siemens (Mitsystem)
	B                      *float64                  `json:"b,omitempty"`                      // optional; CIM: PowerTransformerEnd.b -- Einheit: Siemens (Mitsystem)
	G0                     *float64                  `json:"g0,omitempty"`                     // optional; CIM: PowerTransformerEnd.g0 -- Einheit: Siemens (Nullsystem)
	B0                     *float64                  `json:"b0,omitempty"`                     // optional; CIM: PowerTransformerEnd.b0 -- Einheit: Siemens (Nullsystem)
	ConnectionKind         *string                   `json:"connectionKind,omitempty"`         // optional; CIM: PowerTransformerEnd.connectionKind -- keine Einheit; Schaltgruppe (Y/D/Z)
	PhaseAngleClock        *int                      `json:"phaseAngleClock,omitempty"`        // optional; CIM: PowerTransformerEnd.phaseAngleClock -- keine Einheit; Schaltgruppen-Uhrzeigerzahl
	MaxApparentPowerFactor *float64                  `json:"maxApparentPowerFactor,omitempty"` // optional; CIM: PowerTransformerEnd.maxApparentPowerFactor -- keine Einheit; NSC-Dialekt
}
