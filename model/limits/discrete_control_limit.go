package limits

import (
	"gitlab.com/openk-nsc/jag/model/common"
	"gitlab.com/openk-nsc/jag/model/control"
)

// DiscreteControlLimit is one discrete control step (e.g. one steuVA/
// steuEA switching stage) refining a discrete RegulatingControl.
// CIM: IEC61970 Base "DiscreteControlLimit" (NSC-Dialekt-Nutzung).
type DiscreteControlLimit struct {
	common.IdentifiedObject
	RegulatingControl *control.RegulatingControl `json:"regulatingControl,omitempty"` // optional; CIM: DiscreteControlLimit.RegulatingControl -- keine Einheit
	SequenceNumber    *int                       `json:"sequenceNumber,omitempty"`    // optional; CIM: DiscreteControlLimit.sequenceNumber -- keine Einheit
	Value             *float64                   `json:"value,omitempty"`             // optional; CIM: DiscreteControlLimit.value -- Einheit abhängig vom geregelten Wert
}
