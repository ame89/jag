package hierarchy

import (
	"gitlab.com/openk-nsc/jag/model/common"
	"gitlab.com/openk-nsc/jag/model/geometry"
)

// House is JAG's renamed form of the CIM class "Building" (see
// spec/Idee.md JAG-terminology convention: the underlying CIM class name
// remains "Building"; JAG uses "House" at the Go level only). Represents a
// house-connection's physical building. CIM: IEC61970 Base "Building".
type House struct {
	common.IdentifiedObject
	Location *geometry.Location `json:"location,omitempty"` // optional; CIM: Building (PowerSystemResource).Location -- keine Einheit
}
