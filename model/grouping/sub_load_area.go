package grouping

import (
	"gitlab.com/openk-nsc/jag/model/common"
)

// SubLoadArea is a sub-division of a LoadArea, the direct parent of
// ConformLoadGroup in CIM's own hierarchy. No attributes beyond the base
// IdentifiedObject fields were verified against our example data -- this
// is a minimal placeholder. CIM: IEC61970 Base "SubLoadArea".
type SubLoadArea struct {
	common.IdentifiedObject
	LoadArea *LoadArea `json:"loadArea,omitempty"` // optional; CIM: SubLoadArea.LoadArea -- keine Einheit
}
