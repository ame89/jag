package connectionusers

// ConformLoad is an EnergyConsumer whose demand follows one of a
// utility's standard load profiles/curves (as opposed to a NonConformLoad
// with its own individual profile). CIM: IEC61970 Base "ConformLoad"
// (extends "EnergyConsumer").
type ConformLoad struct {
	EnergyConsumer
	LoadResponse *LoadResponseCharacteristic `json:"loadResponse,omitempty"` // optional; CIM: EnergyConsumer.LoadResponse -- keine Einheit
}
