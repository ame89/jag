package connectionusers

import (
	"gitlab.com/openk-nsc/jag/model/common"
)

// EquivalentInjection is a simplified equivalent source/sink used to
// represent an aggregated part of the grid not otherwise modeled (e.g. in
// bus-branch CGMES test configurations). CIM: IEC61970 Base
// "EquivalentInjection" (extends "ConductingEquipment").
type EquivalentInjection struct {
	common.Equipment
	P    *float64 `json:"p,omitempty"`    // optional; CIM: EquivalentInjection.p -- Einheit: MW
	Q    *float64 `json:"q,omitempty"`    // optional; CIM: EquivalentInjection.q -- Einheit: MVAr
	MinP *float64 `json:"minP,omitempty"` // optional; CIM: EquivalentInjection.minP -- Einheit: MW
	MaxP *float64 `json:"maxP,omitempty"` // optional; CIM: EquivalentInjection.maxP -- Einheit: MW
	MinQ *float64 `json:"minQ,omitempty"` // optional; CIM: EquivalentInjection.minQ -- Einheit: MVAr
	MaxQ *float64 `json:"maxQ,omitempty"` // optional; CIM: EquivalentInjection.maxQ -- Einheit: MVAr
}
