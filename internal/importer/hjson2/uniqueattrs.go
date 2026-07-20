package hjson

// UniqueAttrClass is a static, curated table of CIM "Class.attribute"
// keys whose attribute-name suffix is only ever used by exactly one CIM
// class across every example dataset in examples/{cgmes,cgmes3,cigre_mv,
// nsc} (derived 2026-07-20 by scanning every `cim:Class.attribute` literal
// occurring there — see this package's own history for the extraction
// method). Keyed by the bare attribute name (e.g. "normallyInService"),
// valued by its one known owning class (e.g. "Equipment").
//
// This lets internal/exporter/hjson2's writeAttributesBlock safely strip
// the "Class." prefix from an attribute even when that class is NOT the
// object's own concrete class (e.g. "Equipment.normallyInService" shown on
// a Fuse, Breaker, Meter, ... — "Equipment" is a shared CIM base class,
// not any one concrete class) — as long as the suffix isn't also used by
// some other class for a different meaning, stripping it can't lose
// information: internal/importer/hjson2's denormalizeAttrKey below
// consults this same table to reconstruct the exact full key on import.
//
// This is a curated *seed*, not a guaranteed-exhaustive enumeration of the
// whole CIM/CGMES schema: a suffix absent here (or a class for it not yet
// seen) simply isn't stripped/falls back to "<the object's own concrete
// class>.<name>" — always safe, only ever a missed simplification, never
// a wrong reconstruction. Extend this table as new datasets are analyzed;
// removing an entry could break already-written hjson2 files that used
// the short form, so entries should only ever be added, not renamed.
var UniqueAttrClass = map[string]string{
	"normallyInService":              "Equipment",
	"EquipmentContainer":             "Equipment",
	"InstanceSet":                    "IdentifiedObject",
	"AssetDatasheet":                 "PowerSystemResource",
	"PSRType":                        "PowerSystemResource",
	"nominalCurrent":                 "Fuse",
	"measurementLocationIdentifier":  "Meter",
	"MeasuringSchedule":              "Meter",
	"TransmissionSchedule":           "Meter",
	"disabled":                       "TimeSchedule",
	"recurrencePeriod":               "TimeSchedule",
	"length":                         "Conductor",
	"PerLengthImpedance":             "ACLineSegment",
	"r0":                             "PerLengthSequenceImpedance",
	"x":                              "PerLengthSequenceImpedance",
	"x0":                             "PerLengthSequenceImpedance",
	"nominalVoltage":                 "BaseVoltage",
	"maxP":                           "PowerElectronicsUnit",
	"minP":                           "PowerElectronicsUnit",
	"PowerElectronicsConnection":     "PowerElectronicsUnit",
	"technicalResourceIdentifier":    "PowerElectronicsUnit",
	"controllableResourceIdentifier": "PowerElectronicsConnection",
	"LoadResponse":                   "EnergyConsumer",
	"PowerTransformerEnd":            "PowerTransformer",
	"connectionKind":                 "PowerTransformerEnd",
	"maxApparentPowerFactor":         "PowerTransformerEnd",
	"PowerTransformer":               "PowerTransformerEnd",
	"ratedS":                         "PowerTransformerEnd",
	"tculControlMode":                "RatioTapChanger",
	"controlEnabled":                 "RegulatingCondEq",
	"discrete":                       "RegulatingControl",
	"enabled":                        "RegulatingControl",
	"maxAllowedTargetValue":          "RegulatingControl",
	"minAllowedTargetValue":          "RegulatingControl",
	"mode":                           "RegulatingControl",
	"targetValueUnitMultiplier":      "RegulatingControl",
	"TapChangerControl":              "TapChanger",
	"ConnectivityNode":               "Terminal",
	"SvPowerFlow":                    "Terminal",
	"ratedPower":                     "UsagePoint",
	"Building":                       "UsagePoint",
	"UsagePointLocation":             "UsagePoint",
	"Substation":                     "VoltageLevel",
	"NormalEnergizingSubstation":     "Feeder",
	"unitMultiplier":                 "Measurement",
	"unitSymbol":                     "Measurement",
	"asynchronousMachineType":        "AsynchronousMachine",
	"operatingMode":                  "SynchronousMachine",
	"shortCircuitRotorType":          "SynchronousMachine",
	"InitialReactiveCapabilityCurve": "SynchronousMachine",
	"windGenUnitType":                "WindGeneratingUnit",
	"genControlSource":               "GeneratingUnit",
	"fossilFuelType":                 "FossilFuel",
	"ThermalGeneratingUnit":          "FossilFuel",
	"crsUrn":                         "CoordinateSystem",
	"orientation":                    "Diagram",
	"curveStyle":                     "Curve",
	"xUnit":                          "Curve",
	"y1Unit":                         "Curve",
	"y2Unit":                         "Curve",
	"value":                          "DiscreteControlLimit",
	"direction":                      "OperationalLimitType",
}
