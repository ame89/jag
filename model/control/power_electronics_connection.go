package control

import (
	"gitlab.com/openk-nsc/jag/model/common"
	"gitlab.com/openk-nsc/jag/model/metadata"
)

// PowerElectronicsConnection is the CIM connection-point equipment used
// (in the NSC dialect) to model a "Steuerbox"/EMS-controlled connection
// point (e.g. PV feed-in or a controllable load) -- a JAG Zweipol edge.
// CIM: IEC61970 dynamics "PowerElectronicsConnection" (extends
// "RegulatingCondEq").
type PowerElectronicsConnection struct {
	common.Equipment
	BaseVoltage                    *metadata.BaseVoltage `json:"baseVoltage,omitempty"`                    // optional; CIM: ConductingEquipment.BaseVoltage -- keine Einheit
	ControllableResourceIdentifier *string               `json:"controllableResourceIdentifier,omitempty"` // optional; CIM: PowerElectronicsConnection.controllableResourceIdentifier -- keine Einheit
	ControlEnabled                 *bool                 `json:"controlEnabled,omitempty"`                 // optional; CIM: RegulatingCondEq.controlEnabled -- keine Einheit
	RegulatingControl              *RegulatingControl    `json:"regulatingControl,omitempty"`              // optional; CIM: RegulatingCondEq.RegulatingControl -- keine Einheit
}
