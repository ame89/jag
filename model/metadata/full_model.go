package metadata

import (
	"gitlab.com/openk-nsc/jag/model/common"
)

// FullModel is the CGMES model-header object present once per profile file
// (EQ/SSH/TP/SV/...), describing model version/scope and dependency links
// to other profiles (e.g. TP depends on EQ, via Model.DependentOn).
// CIM: CGMES "md:FullModel".
type FullModel struct {
	common.IdentifiedObject
	ModelingAuthoritySet *string      `json:"modelingAuthoritySet,omitempty"` // optional; CIM: Model.modelingAuthoritySet -- keine Einheit
	Profile              *string      `json:"profile,omitempty"`              // optional; CIM: Model.profile -- keine Einheit
	Version              *string      `json:"version,omitempty"`              // optional; CIM: Model.version -- keine Einheit
	Created              *string      `json:"created,omitempty"`              // optional; CIM: Model.created -- ISO-8601-Zeitstempel
	ScenarioTime         *string      `json:"scenarioTime,omitempty"`         // optional; CIM: Model.scenarioTime -- ISO-8601-Zeitstempel
	DependentOn          []*FullModel `json:"dependentOn,omitempty"`          // optional; CIM: Model.DependentOn -- Referenzen auf abhängige Profile
}
