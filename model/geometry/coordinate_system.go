package geometry

import (
	"gitlab.com/openk-nsc/jag/model/common"
)

// CoordinateSystem describes the reference system (e.g. WGS 84) used by a
// Location's coordinates. CIM: IEC61970 Base "CoordinateSystem".
type CoordinateSystem struct {
	common.IdentifiedObject
	CrsUrn *string `json:"crsUrn,omitempty"` // optional; CIM: CoordinateSystem.crsUrn -- keine Einheit (URN-String, z.B. "urn:ogc:def:crs:EPSG::4326")
}
