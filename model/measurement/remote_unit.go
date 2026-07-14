package measurement

import (
	"gitlab.com/openk-nsc/jag/model/common"
)

// RemoteUnit is a telemetry/SCADA remote terminal unit (RTU). No
// attributes beyond the base IdentifiedObject fields were verified against
// our example data -- this is a minimal placeholder.
// CIM: IEC61970 SCADA "RemoteUnit".
type RemoteUnit struct {
	common.IdentifiedObject
	CommunicationLink *CommunicationLink `json:"communicationLink,omitempty"` // optional; CIM: RemoteUnit.CommunicationLinks -- keine Einheit
}
