package metadata

import (
	"gitlab.com/openk-nsc/jag/model/common"
)

// BasePower is a catalog-like reference object describing a base power
// value (e.g. for per-unit power-flow calculations). No attributes beyond
// the value itself were verified against our example data.
// CIM: IEC61970 Base "BasePower".
type BasePower struct {
	common.IdentifiedObject
	BasePower *float64 `json:"basePower,omitempty"` // optional; CIM: BasePower.basePower -- Einheit: MVA
}
