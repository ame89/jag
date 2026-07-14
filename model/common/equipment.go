package common

// Equipment is the common base embedded by every concrete CIM Equipment
// class (both Zweipol/Edge equipment such as ACLineSegment/Breaker and
// non-edge equipment such as BusbarSection alike): container membership
// plus in-service state flags. CIM: IEC61970 Base "Equipment" (abstract,
// extends "PowerSystemResource").
//
// Every concrete type in model/* that embeds Equipment is, in JAG's
// simplified model, always a ConductingEquipment too -- so the
// ConductingEquipmentRef marker method is implemented here once and
// promoted to every embedder, instead of being redeclared on each of the
// ~20 concrete equipment structs.
type Equipment struct {
	IdentifiedObject
	EquipmentContainer EquipmentContainer `json:"equipmentContainer,omitempty"` // optional; CIM: Equipment.EquipmentContainer -- keine Einheit
	Aggregate          *bool              `json:"aggregate,omitempty"`          // optional; CIM: Equipment.aggregate -- keine Einheit
	NormallyInService  *bool              `json:"normallyInService,omitempty"`  // optional; CIM: Equipment.normallyInService -- keine Einheit
	InService          *bool              `json:"inService,omitempty"`          // optional; CIM: Equipment.inService -- keine Einheit
}

// IsConductingEquipment implements ConductingEquipmentRef for Equipment and
// (via Go's method promotion) for every struct that embeds it.
func (e *Equipment) IsConductingEquipment() {}

var _ ConductingEquipmentRef = (*Equipment)(nil)
