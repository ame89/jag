package measurement

import (
	"gitlab.com/openk-nsc/jag/model/common"
)

// CommunicationLink is the SCADA communication channel connecting one or
// more RemoteUnits. No attributes beyond the base IdentifiedObject fields
// were verified against our example data -- this is a minimal placeholder.
// CIM: IEC61970 SCADA "CommunicationLink".
type CommunicationLink struct {
	common.IdentifiedObject
}
