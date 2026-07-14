package metadata

import (
	"gitlab.com/openk-nsc/jag/model/common"
)

// Name is an additional (possibly non-unique) alias name for an
// IdentifiedObject, categorized via a NameType. CIM: IEC61970 Base "Name".
type Name struct {
	common.IdentifiedObject
	NameType *NameType `json:"nameType,omitempty"` // optional; CIM: Name.NameType -- keine Einheit
}
