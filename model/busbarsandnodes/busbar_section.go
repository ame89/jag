package busbarsandnodes

import (
	"gitlab.com/openk-nsc/jag/model/common"
	"gitlab.com/openk-nsc/jag/model/metadata"
)

// BusbarSection is a physical busbar segment. Despite being modeled as CIM
// Equipment, JAG exposes it as a Node (real connection point), not a
// Zweipol Edge -- see spec/Idee.md Gruppe 4 "Sammelschienen/Knoten" and
// spec/Konzept.md's Busbar-Container decision.
// CIM: IEC61970 Base "BusbarSection" (extends "Connector").
type BusbarSection struct {
	common.Equipment
	BaseVoltage *metadata.BaseVoltage `json:"baseVoltage,omitempty"` // optional; CIM: ConductingEquipment.BaseVoltage -- keine Einheit
	IpMax       *float64              `json:"ipMax,omitempty"`       // optional; CIM: BusbarSection.ipMax -- Einheit: kA (max. zulässiger Stoßkurzschlussstrom)
}
