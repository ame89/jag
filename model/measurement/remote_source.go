package measurement

import (
	"gitlab.com/openk-nsc/jag/model/common"
)

// RemoteSource links a Measurement to the RemoteUnit/telemetry channel it
// arrives on. No attributes beyond the base IdentifiedObject fields were
// verified against our example data -- this is a minimal placeholder.
// CIM: IEC61970 SCADA "RemoteSource" (extends "RemotePoint").
type RemoteSource struct {
	common.IdentifiedObject
	RemoteUnit *RemoteUnit `json:"remoteUnit,omitempty"` // optional; CIM: RemotePoint.RemoteUnit -- keine Einheit
}
