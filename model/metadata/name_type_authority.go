package metadata

import (
	"gitlab.com/openk-nsc/jag/model/common"
)

// NameTypeAuthority is a catalog entry identifying the organization that
// defines/maintains a set of NameType categories.
// CIM: IEC61970 Base "NameTypeAuthority" (catalog).
type NameTypeAuthority struct {
	common.IdentifiedObject
}
