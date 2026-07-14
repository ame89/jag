package statevariables

import (
	"gitlab.com/openk-nsc/jag/model/common"
)

// SvVoltage is a state-variable (calculation result) object holding the
// voltage-magnitude/angle result at one TopologicalNode, as produced by a
// CGMES SV profile -- not a live measurement.
// CIM: CGMES SV profile "SvVoltage".
type SvVoltage struct {
	common.IdentifiedObject
	V     *float64 `json:"v,omitempty"`     // optional; CIM: SvVoltage.v -- Einheit: kV; Standard-CIM-Attribut, nicht einzeln gegen unsere Beispieldaten verifiziert
	Angle *float64 `json:"angle,omitempty"` // optional; CIM: SvVoltage.angle -- Einheit: Grad; Standard-CIM-Attribut, nicht einzeln gegen unsere Beispieldaten verifiziert
}
