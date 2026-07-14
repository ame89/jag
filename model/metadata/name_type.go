package metadata

import (
	"gitlab.com/openk-nsc/jag/model/common"
)

// NameType is a catalog entry describing a category of alias Name objects
// (e.g. "GIS asset ID"). CIM: IEC61970 Base "NameType" (catalog).
type NameType struct {
	common.IdentifiedObject
	NameTypeAuthority *NameTypeAuthority `json:"nameTypeAuthority,omitempty"` // optional; CIM: NameType.NameTypeAuthority -- keine Einheit
}
