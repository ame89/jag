package switchgear

import (
	"gitlab.com/openk-nsc/jag/model/common"
	"gitlab.com/openk-nsc/jag/model/metadata"
)

// Switch is the generic CIM switching-device base (e.g. a Trenner in the
// NSC dialect). Closed switches are treated as zero-ohm (collapsed) in
// JAG's electrical topology view; open switches interrupt it (see
// spec/Konzept.md Topologie-Kapitel). CIM: IEC61970 Base "Switch" (extends
// "ConductingEquipment").
type Switch struct {
	common.Equipment
	BaseVoltage  *metadata.BaseVoltage `json:"baseVoltage,omitempty"`  // optional; CIM: ConductingEquipment.BaseVoltage -- keine Einheit
	NormalOpen   *bool                 `json:"normalOpen,omitempty"`   // optional; CIM: Switch.normalOpen -- keine Einheit
	Open         *bool                 `json:"open,omitempty"`         // optional; CIM: Switch.open -- keine Einheit (tatsächlicher, nicht nur normaler Schaltzustand)
	Retained     *bool                 `json:"retained,omitempty"`     // optional; CIM: Switch.retained -- keine Einheit
	Locked       *bool                 `json:"locked,omitempty"`       // optional; CIM: Switch.locked -- keine Einheit
	RatedCurrent *float64              `json:"ratedCurrent,omitempty"` // optional; CIM: Switch.ratedCurrent -- Einheit: Ampere (A)
}
