package limits

import (
	"gitlab.com/openk-nsc/jag/model/common"
)

// OperationalLimitType is a catalog entry describing the kind of limit
// (e.g. "patl" = Permanent Admissible Transmission Loading, "tatl" =
// Temporary Admissible Transmission Loading) referenced by CurrentLimit/
// VoltageLimit. Encoded in the raw CIM/CGMES data as an rdf:resource enum
// reference, not a literal string (see pandapower/README.md for how this
// was discovered empirically). CIM: IEC61970 Base "OperationalLimitType"
// (catalog).
type OperationalLimitType struct {
	common.IdentifiedObject
	Kind *string `json:"kind,omitempty"` // optional; CIM: OperationalLimitType.kind -- keine Einheit (Enum-Wert, z.B. "patl"/"tatl")
}
