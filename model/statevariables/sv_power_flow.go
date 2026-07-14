package statevariables

import (
	"gitlab.com/openk-nsc/jag/model/common"
)

// SvPowerFlow is a state-variable (calculation result) object holding the
// power-flow result (P/Q) at one Terminal, as produced by a CGMES SV
// profile (e.g. from an external load-flow run) -- not a live measurement
// (JAG does not ingest live telemetry, see spec/Konzept.md).
// CIM: CGMES SV profile "SvPowerFlow".
type SvPowerFlow struct {
	common.IdentifiedObject
	P *float64 `json:"p,omitempty"` // optional; CIM: SvPowerFlow.p -- Einheit: MW; Standard-CIM-Attribut, nicht einzeln gegen unsere Beispieldaten verifiziert
	Q *float64 `json:"q,omitempty"` // optional; CIM: SvPowerFlow.q -- Einheit: MVAr; Standard-CIM-Attribut, nicht einzeln gegen unsere Beispieldaten verifiziert
}
