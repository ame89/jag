package busbarsandnodes

import (
	"gitlab.com/openk-nsc/jag/model/common"
	"gitlab.com/openk-nsc/jag/model/limits"
	"gitlab.com/openk-nsc/jag/model/statevariables"
)

// Terminal is a CIM connection point of one piece of ConductingEquipment.
// JAG deliberately avoids modeling Terminals explicitly wherever possible
// (each JAG Edge just directly references its two connections instead) --
// this struct exists for lossless CIM import/export round-tripping.
// ACDCTerminal.sequenceNumber (1 or 2) maps directly onto JAG's own
// convention: 1 = toward the higher voltage level / toward the
// transformer, 2 = toward ground/earth potential (see spec/Idee.md).
// CIM: IEC61970 Base "Terminal" (extends "ACDCTerminal").
type Terminal struct {
	common.IdentifiedObject
	ConductingEquipment common.ConductingEquipmentRef `json:"conductingEquipment,omitempty"` // optional; CIM: Terminal.ConductingEquipment -- keine Einheit
	ConnectivityNode    *ConnectivityNode             `json:"connectivityNode,omitempty"`    // optional; CIM: Terminal.ConnectivityNode -- keine Einheit
	TopologicalNode     *TopologicalNode              `json:"topologicalNode,omitempty"`     // optional; CIM: Terminal.TopologicalNode -- keine Einheit
	SvPowerFlow         *statevariables.SvPowerFlow   `json:"svPowerFlow,omitempty"`         // optional; CIM: Terminal.SvPowerFlow -- keine Einheit; Lastfluss-Rechenergebnis, keine Live-Messung
	SequenceNumber      *int                          `json:"sequenceNumber,omitempty"`      // optional; CIM: ACDCTerminal.sequenceNumber -- keine Einheit; 1 oder 2, siehe JAG-Terminal-Konvention oben
	Connected           *bool                         `json:"connected,omitempty"`           // optional; CIM: ACDCTerminal.connected -- keine Einheit
	BusNameMarker       *BusNameMarker                `json:"busNameMarker,omitempty"`       // optional; CIM: ACDCTerminal.BusNameMarker -- keine Einheit
	OperationalLimitSet *limits.OperationalLimitSet   `json:"operationalLimitSet,omitempty"` // optional; CIM: ACDCTerminal.OperationalLimitSet -- keine Einheit
	Phases              *string                       `json:"phases,omitempty"`              // optional; CIM: Terminal.phases -- keine Einheit (in Testdaten immer "ABC", da JAG 1-phasig vereinfacht)
}
