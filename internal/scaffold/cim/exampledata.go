package cim

// classExamples is the curated, per-CIM-class table of illustrative
// example values used by ScaffoldExample (see scaffold.go's valueFor):
// classExamples["Substation"]["IdentifiedObject.name"] etc. Each value is
// the literal HJSON text to render (so string values already include
// their own quotes, e.g. `"ONS-1"`, while numbers/bools are bare, e.g.
// `0.8`/`false`) — this lets a curated entry also carry an explanatory
// inline "// ..." suffix where useful (e.g. unit conversions) without
// needing a separate mechanism.
//
// Only classes actually used by the four composite scaffolds
// (generateStation/generateHouse/generateKabel, elements.go) are curated
// here; every other class/key not listed simply falls back to
// genericExample's type-derived placeholder. IMPORTANT — per this
// package's binding rule (see .github/copilot-instructions.md's HJSON
// section): whenever a cimdata/*.hjson attribute is added, renamed, or
// removed for one of these classes, this table must be reviewed too, or a
// stale/missing entry will silently fall back to a less illustrative
// generic placeholder.
var classExamples = map[string]map[string]string{
	"Substation": {
		"IdentifiedObject.mRID":        `"ONS-1"`,
		"IdentifiedObject.name":        `"ONS-1 Musterstraße"`,
		"IdentifiedObject.description": `"Ortsnetzstation Musterstraße, Baujahr 2018"`,
		"IdentifiedObject.shortName":   `"ONS-1"`,
		"station_kind":                 `"Ortsnetzstation"`,
		"region":                       `"Nord"`,
	},
	"BusbarSection": {
		"IdentifiedObject.mRID": `"ONS-1-SS-1-1"`,
		"IdentifiedObject.name": `"Sammelschiene 1, Abschnitt 1"`,
		"Equipment.aggregate":   "false",
		"Equipment.inService":   "true",
	},
	"Bay": {
		"IdentifiedObject.mRID": `"ONS-1-Einspeisefeld"`,
		"IdentifiedObject.name": `"Einspeisefeld"`,
	},
	"Disconnector": {
		"IdentifiedObject.mRID": `"ONS-1-Trenner-1"`,
		"IdentifiedObject.name": `"Trenner Einspeisung"`,
		"Switch.normalOpen":     "false // im Normalbetrieb geschlossen (Nullohm-Verbindung)",
		"Equipment.aggregate":   "false",
		"Equipment.inService":   "true",
	},
	"Fuse": {
		"IdentifiedObject.mRID":        `"ONS-1-Sicherung-1"`,
		"IdentifiedObject.name":        `"Sicherung Abgang 1"`,
		"IdentifiedObject.description": `"NH-Sicherung 63A"`,
		"Switch.normalOpen":            "false // nicht ausgelöst (Nullohm-Verbindung)",
		"Switch.ratedCurrent":          "63.0",
		"Fuse.nominalCurrent":          "63.0",
		"Equipment.inService":          "true",
		"Equipment.normallyInService":  "true",
	},
	"PowerTransformer": {
		"IdentifiedObject.mRID":                "\"ONS-1-Trafo-1\"",
		"IdentifiedObject.name":                 "\"Transformator 1\"",
		"IdentifiedObject.description":          "\"800 kVA Verteiltransformator\"",
		"Equipment.aggregate":                   "false",
		"Equipment.normallyInService":            "true",
		"PowerTransformer.isPartOfGeneratorUnit": "false",
	},
	"PowerTransformerEnd": {
		"IdentifiedObject.name":       `"Trafo 1, Wicklung OS"`,
		"TransformerEnd.endNumber":    "1 // 1 = Oberspannungsseite (OS); für die US-Seite ein zweites PowerTransformerEnd mit endNumber=2 anlegen",
		"TransformerEnd.grounded":     "false",
		"PowerTransformerEnd.ratedU":  "20.0 // Beispiel: 20 kV Mittelspannungsseite (OS); bei der US-Seite z.B. 0.4",
		"PowerTransformerEnd.ratedS":  "0.8 // 800 kVA = 0.8 MVA",
		"PowerTransformerEnd.r":       "1.5",
		"PowerTransformerEnd.x":       "6.0",
	},
	"EnergyConsumer": {
		"IdentifiedObject.mRID": `"Haus-1-Verbraucher-1"`,
		"IdentifiedObject.name": `"Hausanschluss Verbraucher"`,
		"EnergyConsumer.p":      "0.005 // 5 kW Bezugsleistung",
		"EnergyConsumer.q":      "0.001",
		"Equipment.aggregate":   "false",
	},
	"PowerElectronicsConnection": {
		"IdentifiedObject.name":                              `"PV-Netzanschluss"`,
		"PowerElectronicsConnection.p":                        "-0.008 // negativ = Einspeisung, hier 8 kW PV-Leistung",
		"PowerElectronicsConnection.q":                        "0.0",
		"RegulatingCondEq.controlEnabled":                     "false",
		"Equipment.normallyInService":                         "true",
	},
	"PhotoVoltaicUnit": {
		"IdentifiedObject.name":                `"PV-Anlage Dach Süd"`,
		"PowerElectronicsUnit.maxP":             "0.008 // 8 kWp",
		"PowerElectronicsUnit.minP":             "0.0",
		"Equipment.normallyInService":           "true",
	},
	"Meter": {
		"IdentifiedObject.name":               `"Zähler Hausanschluss"`,
		"Meter.measurementLocationIdentifier":  `"DE0000000000000000000000000000001"`,
		"Equipment.normallyInService":          "true",
	},
	"Building": {
		"IdentifiedObject.name": `"Musterstraße 1, 12345 Musterstadt"`,
	},
	"ACLineSegment": {
		"IdentifiedObject.mRID":         `"Kabel-1-Abschnitt-1"`,
		"IdentifiedObject.name":         `"NAYY 4x150 Musterstraße 1-3"`,
		"Conductor.length":              "45.0 // Streckenlänge in m/km je nach Projektkonvention",
		"ACLineSegment.r":               "0.206",
		"ACLineSegment.x":               "0.08",
		"Equipment.aggregate":           "false",
		"Equipment.inService":           "true",
		"Equipment.normallyInService":   "true",
	},
	"Junction": {
		"IdentifiedObject.mRID": `"Kabel-1-Muffe-1"`,
		"IdentifiedObject.name": `"Muffe M-1"`,
	},
	"PerLengthSequenceImpedance": {
		"IdentifiedObject.name":              `"NAYY 4x150"`,
		"PerLengthSequenceImpedance.r":       "0.206 // Ω/km",
		"PerLengthSequenceImpedance.x":       "0.08 // Ω/km",
		"PerLengthSequenceImpedance.r0":      "0.824 // Ω/km",
		"PerLengthSequenceImpedance.x0":      "0.32 // Ω/km",
	},
}
