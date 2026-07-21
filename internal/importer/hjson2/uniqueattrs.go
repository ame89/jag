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
	"mRID":       "IdentifiedObject",
	"locked":     "Switch",
	"normalOpen": "Switch",
	"retained":   "Switch",
	// "open" -> "Switch" and "inService" -> "Equipment" (below) are safe
	// despite "SvSwitch.open"/"SvStatus.inService" also occurring in real
	// CGMES data (examples/cgmes3/{Svedala,MicroGrid,SmallGrid,MiniGrid},
	// examples/cgmes/ReliCapGrid_Espheim's SV profile): denormalizeAttrKey
	// (internal/importer/hjson2/resolve.go) special-cases any owner whose
	// own concrete class starts with "Sv" (an SV-profile satellite, e.g.
	// SvSwitch/SvStatus) and skips this table for those, using the
	// satellite's own class instead — so the two contexts never collide.
	// Before that guard existed, "open" was deliberately excluded here,
	// which meant a Switch's *live* SSH-profile open/closed state
	// survived reimport only as "Switch.normalOpen" (its design-time
	// default) — silently wrong whenever a switch's live state differs
	// from its default, which materially changed BuildCircuits' zero-ohm
	// reduction results. Root-caused and fixed 2026-07-21 while
	// investigating a busbar/circuit topology divergence in the
	// ReliCapGrid_Espheim round-trip (48 vs. 303 circuits).
	"open":                          "Switch",
	"inService":                     "Equipment",
	"normallyInService":             "Equipment",
	"EquipmentContainer":            "Equipment",
	"InstanceSet":                   "IdentifiedObject",
	"AssetDatasheet":                "PowerSystemResource",
	"PSRType":                       "PowerSystemResource",
	"nominalCurrent":                "Fuse",
	"measurementLocationIdentifier": "Meter",
	"MeasuringSchedule":             "Meter",
	"TransmissionSchedule":          "Meter",
	"disabled":                      "TimeSchedule",
	"recurrencePeriod":              "TimeSchedule",
	"length":                        "Conductor",
	"PerLengthImpedance":            "ACLineSegment",
	// NOTE: "r0"/"x"/"x0" are deliberately NOT added here even though
	// PerLengthSequenceImpedance.{r0,x,x0} is common in NSC data — real
	// CGMES data (e.g. examples/cgmes/ReliCapGrid_Espheim) instead puts
	// "x" directly on ACLineSegment itself (ACLineSegment.x), so "x"
	// alone is genuinely ambiguous across dialects actually seen in the
	// wild. Adding it would risk a *wrong* reconstruction (not just a
	// missed simplification). Confirmed 2026-07-21 during the
	// ReliCapGrid_Espheim round-trip investigation.
	"nominalVoltage": "BaseVoltage",
	// "maxP"/"minP" ARE mapped to PowerElectronicsUnit (its base class),
	// even though ExternalNetworkInjection.{maxP,minP} also exists in the
	// wild (examples/cgmes/ReliCapGrid_Espheim, examples/cgmes3/MiniGrid)
	// — a genuine but accepted ambiguity, same category as the p/q/ratedU
	// group below. This direction was chosen deliberately: NSC's
	// PowerElectronicsUnit subtypes (PhotoVoltaicUnit, BatteryUnit, ...,
	// see examples/nsc/example_as_cim.xml) always carry maxP/minP as a
	// *base*-class attribute on a *subclass* object, so the own-class
	// fallback alone can never reconstruct it correctly there — whereas
	// ExternalNetworkInjection is never itself subclassed, so its
	// maxP/minP would reconstruct correctly via the own-class fallback if
	// this entry were absent. Keeping the entry fixes the common/larger
	// PowerElectronicsUnit case at the cost of a rare, harmless
	// class-prefix imprecision on ExternalNetworkInjection.maxP/minP
	// (values unaffected). Confirmed 2026-07-21; reverted after breaking
	// TestExportImportRoundTrip's Wallbox/PowerElectronicsUnit.maxP case.
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
