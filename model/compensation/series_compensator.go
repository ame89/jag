package compensation

import (
	"gitlab.com/openk-nsc/jag/model/common"
)

// SeriesCompensator is a series capacitor/reactor -- a Zweipol edge with
// fixed impedance in series with a line. No attributes beyond the base
// Equipment fields were verified against our example data -- this is a
// minimal placeholder. CIM: IEC61970 Base "SeriesCompensator" (extends
// "ConductingEquipment").
type SeriesCompensator struct {
	common.Equipment
}
