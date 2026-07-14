package connectionusers

import (
	"gitlab.com/openk-nsc/jag/model/common"
)

// ExternalNetworkInjection represents the equivalent feed-in from the
// upstream grid at a model boundary (e.g. the HV/MV interface in a
// bus-branch CGMES model). CIM: IEC61970 Base
// "ExternalNetworkInjection" (extends "RegulatingCondEq").
type ExternalNetworkInjection struct {
	common.Equipment
	GovernorSCD *float64 `json:"governorSCD,omitempty"` // optional; CIM: ExternalNetworkInjection.governorSCD -- Einheit: MW/Hz
	P           *float64 `json:"p,omitempty"`           // optional; CIM: ExternalNetworkInjection.p -- Einheit: MW
	Q           *float64 `json:"q,omitempty"`           // optional; CIM: ExternalNetworkInjection.q -- Einheit: MVAr
}
