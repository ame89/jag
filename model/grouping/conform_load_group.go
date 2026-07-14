package grouping

import (
	"gitlab.com/openk-nsc/jag/model/common"
	"gitlab.com/openk-nsc/jag/model/connectionusers"
)

// ConformLoadGroup groups ConformLoad objects that share the same standard
// load-profile scaling within a LoadArea/SubLoadArea. No attributes beyond
// the member list were verified against our example data -- this is a
// minimal placeholder. CIM: IEC61970 Base "ConformLoadGroup" (extends
// "LoadGroup").
type ConformLoadGroup struct {
	common.IdentifiedObject
	EnergyConsumers []*connectionusers.ConformLoad `json:"energyConsumers,omitempty"` // optional; CIM: ConformLoadGroup.EnergyConsumers -- keine Einheit
}
