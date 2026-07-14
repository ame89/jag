package control

import (
	"gitlab.com/openk-nsc/jag/model/common"
)

// TimeSchedule describes a recurring measurement/control schedule (e.g. a
// Meter's reading interval, MeasuringSchedule/TransmissionSchedule).
// CIM: IEC61970 Base "TimeSchedule".
type TimeSchedule struct {
	common.IdentifiedObject
	Disabled         *bool   `json:"disabled,omitempty"`         // optional; CIM: TimeSchedule.disabled -- keine Einheit
	RecurrencePeriod *string `json:"recurrencePeriod,omitempty"` // optional; CIM: TimeSchedule.recurrencePeriod -- Einheit: i.d.R. Sekunden (dialektabhängig als Dauer-String)
}
