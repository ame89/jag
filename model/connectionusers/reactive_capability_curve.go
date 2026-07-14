package connectionusers

import (
	"gitlab.com/openk-nsc/jag/model/common"
)

// ReactiveCapabilityCurve describes a SynchronousMachine's P/Qmin/Qmax
// operating envelope as a set of CurveData points. No attributes beyond
// the point list were verified against our example data -- this is a
// minimal placeholder. CIM: IEC61970 Base "ReactiveCapabilityCurve"
// (extends "Curve").
type ReactiveCapabilityCurve struct {
	common.IdentifiedObject
	CurveData []*CurveData `json:"curveData,omitempty"` // optional; CIM: Curve.CurveDatas -- keine Einheit
}
