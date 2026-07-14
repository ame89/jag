import os

ROOT = os.path.join(os.path.dirname(os.path.dirname(os.path.abspath(__file__))), "model")

files = {}

def add(path, content):
    files[path] = content

MOD = "gitlab.com/openk-nsc/jag/model"

# ---------------------------------------------------------------------------
# model/doc.go
# ---------------------------------------------------------------------------
add("doc.go", '''// Package model contains plain Go structs mirroring the CIM element types
// documented in spec/Idee.md's "Fachliche Gruppierung der beobachteten
// CIM-Elementtypen" section. It exists to let Go code build/hold a CIM
// model in memory (e.g. for hand-written test fixtures or debugging tools),
// independent of JAG's own core node-edge model (internal/core, internal/impl).
//
// # Layout
//
// One subpackage per CIM element group from spec/Idee.md (switchgear,
// lines, transformers, busbarsandnodes, connectionusers, compensation,
// limits, control, hierarchy, geometry, statevariables, metadata,
// measurement, grouping), plus a shared "common" package with the base
// types every CIM class embeds (IdentifiedObject, Equipment) and the marker
// interfaces used for polymorphic references (EquipmentContainer,
// ConnectivityNodeContainer, ConductingEquipmentRef).
//
// One CIM class = one Go struct = one file (e.g. model/switchgear/breaker.go).
//
// # References are pointers, not IDs
//
// Where the raw CIM data uses an rdf:resource reference to another object,
// the corresponding Go struct field is a typed pointer (or, where CIM
// itself is polymorphic - e.g. Equipment.EquipmentContainer can be a
// Substation, VoltageLevel, Bay, Line, or Feeder - a small marker
// interface implemented by all of them) instead of a reference/ID string.
//
// # Avoiding JSON-marshal cycles
//
// Some CIM relationships are naturally bidirectional (e.g.
// PowerTransformer.PowerTransformerEnd forward-references its ends, while
// PowerTransformerEnd.PowerTransformer references back). Serializing both
// directions with encoding/json would recurse forever. Convention used
// throughout this package: the "child points to its parent/owner" direction
// (e.g. PowerTransformerEnd.PowerTransformer, Terminal.TopologicalNode) is a
// normal JSON field; the complementary "parent points down to its
// children/contents" direction (e.g. PowerTransformer.Ends,
// TopologicalNode.Terminals) is still a real Go field for in-memory
// navigation, but is tagged `json:"-"` and therefore excluded from
// serialization.
//
// # Optional attributes
//
// Almost every CIM attribute is optional in practice (frequently absent in
// real-world export files). To make this visible in Go, all scalar
// attributes beyond the base IdentifiedObject.MRID/.Name are modeled as
// pointer types (*string/*float64/*int/*bool) with a `json:",omitempty"`
// tag: a nil pointer means "attribute not set in the source data", as
// opposed to a present zero value. Struct/slice reference fields are
// pointers/slices already and are optional by nature (nil/empty = not set).
//
// # Provenance of the attribute lists
//
// Field lists come from spec/Idee.md's "Beobachtete CIM-Attribute je
// Elementtyp" / "Beobachtete Attribute in examples/nsc/" sections (i.e.
// attributes actually seen in the example datasets under examples/), not
// the full official CIM/CGMES class definition. Where spec/Idee.md
// documents no attributes for a class beyond the base IdentifiedObject
// fields, the struct's doc comment says so explicitly ("keine Attribute in
// den Beispieldaten belegt").
package model
''')

def go_header(pkg, imports):
    imports = imports or []
    imp_lines = "\n".join(f'\t"{i}"' for i in imports)
    if imports:
        return f"package {pkg}\n\nimport (\n{imp_lines}\n)\n\n"
    return f"package {pkg}\n\n"

# ---------------------------------------------------------------------------
# model/common
# ---------------------------------------------------------------------------

add("common/identified_object.go", '''// Package common holds the CIM base types embedded by (almost) every other
// struct in model/*, plus the marker interfaces used for polymorphic
// references (EquipmentContainer, ConnectivityNodeContainer,
// ConductingEquipmentRef). It intentionally has zero dependencies on any
// other model/* subpackage, so that every other subpackage can depend on
// common without risking an import cycle. See model/doc.go for the overall
// package layout and conventions.
package common

// IdentifiedObject is the CIM base type embedded by (almost) every CIM
// class: a stable identifier plus optional human-readable name/description.
// CIM: IEC61970 Base "IdentifiedObject".
type IdentifiedObject struct {
\tMRID        string  `json:"mRID"`                  // CIM: IdentifiedObject.mRID -- global eindeutige ID (RDF ID/UUID), keine Einheit
\tName        string  `json:"name,omitempty"`         // CIM: IdentifiedObject.name -- keine Einheit
\tDescription *string `json:"description,omitempty"` // optional; CIM: IdentifiedObject.description -- keine Einheit
}
''')

add("common/interfaces.go", '''package common

// EquipmentContainer is implemented by every CIM class that can own
// Equipment (e.g. Substation, VoltageLevel, Bay, Line, Feeder). Equipment
// references its container through this interface instead of a generic
// ID/reference string, per CIM's own EquipmentContainer abstraction --
// this keeps model/hierarchy, model/lines etc. free to add new container
// types without model/common needing to know about them.
// CIM: IEC61970 Base "EquipmentContainer" (abstract).
type EquipmentContainer interface {
\tIsEquipmentContainer()
}

// ConnectivityNodeContainer is implemented by CIM classes that can contain
// ConnectivityNode/TopologicalNode objects (VoltageLevel, Bay, Line, ...).
// CIM: IEC61970 Base "ConnectivityNodeContainer" (abstract).
type ConnectivityNodeContainer interface {
\tIsConnectivityNodeContainer()
}

// ConductingEquipmentRef is implemented by every concrete CIM
// ConductingEquipment class (Breaker, ACLineSegment, PowerTransformer, ...)
// so that Terminal.ConductingEquipment can hold a typed pointer to any of
// them without model/busbarsandnodes needing to import every equipment
// package (which would create an import cycle, since several equipment
// packages need to reference Terminal).
// CIM: IEC61970 Base "ConductingEquipment" (abstract).
type ConductingEquipmentRef interface {
\tIsConductingEquipment()
}
''')

add("common/equipment.go", '''package common

// Equipment is the common base embedded by every concrete CIM Equipment
// class (both Zweipol/Edge equipment such as ACLineSegment/Breaker and
// non-edge equipment such as BusbarSection alike): container membership
// plus in-service state flags. CIM: IEC61970 Base "Equipment" (abstract,
// extends "PowerSystemResource").
//
// Every concrete type in model/* that embeds Equipment is, in JAG's
// simplified model, always a ConductingEquipment too -- so the
// ConductingEquipmentRef marker method is implemented here once and
// promoted to every embedder, instead of being redeclared on each of the
// ~20 concrete equipment structs.
type Equipment struct {
\tIdentifiedObject
\tEquipmentContainer EquipmentContainer `json:"equipmentContainer,omitempty"` // optional; CIM: Equipment.EquipmentContainer -- keine Einheit
\tAggregate          *bool              `json:"aggregate,omitempty"`          // optional; CIM: Equipment.aggregate -- keine Einheit
\tNormallyInService  *bool              `json:"normallyInService,omitempty"`  // optional; CIM: Equipment.normallyInService -- keine Einheit
\tInService          *bool              `json:"inService,omitempty"`          // optional; CIM: Equipment.inService -- keine Einheit
}

// IsConductingEquipment implements ConductingEquipmentRef for Equipment and
// (via Go's method promotion) for every struct that embeds it.
func (e *Equipment) IsConductingEquipment() {}

var _ ConductingEquipmentRef = (*Equipment)(nil)
''')

# ---------------------------------------------------------------------------
# model/metadata  (imports: common)
# ---------------------------------------------------------------------------
_imp_common = [f"{MOD}/common"]

add("metadata/full_model.go", go_header("metadata", _imp_common) + '''// FullModel is the CGMES model-header object present once per profile file
// (EQ/SSH/TP/SV/...), describing model version/scope and dependency links
// to other profiles (e.g. TP depends on EQ, via Model.DependentOn).
// CIM: CGMES "md:FullModel".
type FullModel struct {
\tcommon.IdentifiedObject
\tModelingAuthoritySet *string      `json:"modelingAuthoritySet,omitempty"` // optional; CIM: Model.modelingAuthoritySet -- keine Einheit
\tProfile              *string      `json:"profile,omitempty"`              // optional; CIM: Model.profile -- keine Einheit
\tVersion              *string      `json:"version,omitempty"`              // optional; CIM: Model.version -- keine Einheit
\tCreated              *string      `json:"created,omitempty"`              // optional; CIM: Model.created -- ISO-8601-Zeitstempel
\tScenarioTime         *string      `json:"scenarioTime,omitempty"`         // optional; CIM: Model.scenarioTime -- ISO-8601-Zeitstempel
\tDependentOn          []*FullModel `json:"dependentOn,omitempty"`          // optional; CIM: Model.DependentOn -- Referenzen auf abhängige Profile
}
''')

add("metadata/instance_set.go", go_header("metadata", _imp_common) + '''// InstanceSet replaces FullModel in the NSC dialect: a lightweight "this
// file/data set" marker object referenced by (almost) every NSC object via
// IdentifiedObject.InstanceSet. Unlike CGMES FullModel it carries no
// profile/version/dependency metadata (see spec/Idee.md, NSC-Dialekt hat
// keinen Modell-Metadaten-Header).
// CIM: NSC-Dialekt "InstanceSet".
type InstanceSet struct {
\tcommon.IdentifiedObject
}
''')

add("metadata/base_voltage.go", go_header("metadata", _imp_common) + '''// BaseVoltage is a catalog-like reference object describing one nominal
// voltage level (e.g. 20 kV), shared by all VoltageLevel/ConductingEquipment
// objects at that level. CIM: IEC61970 Base "BaseVoltage".
type BaseVoltage struct {
\tcommon.IdentifiedObject
\tNominalVoltage *float64 `json:"nominalVoltage,omitempty"` // optional; CIM: BaseVoltage.nominalVoltage -- Einheit: kV
}
''')

add("metadata/base_power.go", go_header("metadata", _imp_common) + '''// BasePower is a catalog-like reference object describing a base power
// value (e.g. for per-unit power-flow calculations). No attributes beyond
// the value itself were verified against our example data.
// CIM: IEC61970 Base "BasePower".
type BasePower struct {
\tcommon.IdentifiedObject
\tBasePower *float64 `json:"basePower,omitempty"` // optional; CIM: BasePower.basePower -- Einheit: MVA
}
''')

add("metadata/boundary_point.go", go_header("metadata", _imp_common) + '''// BoundaryPoint marks a CGMES model-boundary connection point (where one
// partial model's data ends and an external/boundary equivalent begins).
// No attributes beyond the base IdentifiedObject fields were verified
// against our example data -- this is a minimal placeholder.
// CIM: CGMES Boundary profile "BoundaryPoint".
type BoundaryPoint struct {
\tcommon.IdentifiedObject
}
''')

add("metadata/name.go", go_header("metadata", _imp_common) + '''// Name is an additional (possibly non-unique) alias name for an
// IdentifiedObject, categorized via a NameType. CIM: IEC61970 Base "Name".
type Name struct {
\tcommon.IdentifiedObject
\tNameType *NameType `json:"nameType,omitempty"` // optional; CIM: Name.NameType -- keine Einheit
}
''')

add("metadata/name_type.go", go_header("metadata", _imp_common) + '''// NameType is a catalog entry describing a category of alias Name objects
// (e.g. "GIS asset ID"). CIM: IEC61970 Base "NameType" (catalog).
type NameType struct {
\tcommon.IdentifiedObject
\tNameTypeAuthority *NameTypeAuthority `json:"nameTypeAuthority,omitempty"` // optional; CIM: NameType.NameTypeAuthority -- keine Einheit
}
''')

add("metadata/name_type_authority.go", go_header("metadata", _imp_common) + '''// NameTypeAuthority is a catalog entry identifying the organization that
// defines/maintains a set of NameType categories.
// CIM: IEC61970 Base "NameTypeAuthority" (catalog).
type NameTypeAuthority struct {
\tcommon.IdentifiedObject
}
''')

# ---------------------------------------------------------------------------
# model/geometry  (imports: common)
# ---------------------------------------------------------------------------
add("geometry/location.go", go_header("geometry", _imp_common) + '''// Location is a geographic/postal location attached to a
// PowerSystemResource (e.g. Substation, House/Building), holding either
// coordinates (via PositionPoint) or a postal address. JAG itself only
// stores 2D WGS-84 coordinates (see spec/Konzept.md, Geometrie-Kapitel),
// but the raw CIM Location can also carry a plain address.
// CIM: IEC61970 Base "Location".
type Location struct {
\tcommon.IdentifiedObject
\tCoordinateSystem *CoordinateSystem `json:"coordinateSystem,omitempty"` // optional; CIM: Location.CoordinateSystem -- keine Einheit
\tMainAddress      *string           `json:"mainAddress,omitempty"`      // optional; CIM: Location.mainAddress -- keine Einheit
\tPositionPoints   []*PositionPoint  `json:"positionPoints,omitempty"`   // optional; CIM: Location.PositionPoints -- keine Einheit
}
''')

add("geometry/position_point.go", go_header("geometry", None) + '''// PositionPoint is one coordinate (x/y, optionally sequenced) of a
// Location's geometry (e.g. one vertex of a cable route).
// CIM: IEC61970 Base "PositionPoint".
type PositionPoint struct {
\tLocation       *Location `json:"-"`                         // back-reference to owning Location, excluded from JSON to avoid cycles (see Location.PositionPoints)
\tSequenceNumber *int      `json:"sequenceNumber,omitempty"`  // optional; CIM: PositionPoint.sequenceNumber -- keine Einheit
\tXPosition      *float64  `json:"xPosition,omitempty"`       // optional; CIM: PositionPoint.xPosition -- Einheit je CoordinateSystem, i.d.R. Längengrad (WGS 84)
\tYPosition      *float64  `json:"yPosition,omitempty"`       // optional; CIM: PositionPoint.yPosition -- Einheit je CoordinateSystem, i.d.R. Breitengrad (WGS 84)
}
''')

add("geometry/coordinate_system.go", go_header("geometry", _imp_common) + '''// CoordinateSystem describes the reference system (e.g. WGS 84) used by a
// Location's coordinates. CIM: IEC61970 Base "CoordinateSystem".
type CoordinateSystem struct {
\tcommon.IdentifiedObject
\tCrsUrn *string `json:"crsUrn,omitempty"` // optional; CIM: CoordinateSystem.crsUrn -- keine Einheit (URN-String, z.B. "urn:ogc:def:crs:EPSG::4326")
}
''')

add("geometry/diagram.go", go_header("geometry", _imp_common) + '''// Diagram is a named diagram/drawing (e.g. a single-line diagram) that
// groups DiagramObject entries. No attributes beyond the base
// IdentifiedObject fields were verified against our example data.
// CIM: IEC61970 Diagram Layout "Diagram".
type Diagram struct {
\tcommon.IdentifiedObject
}
''')

add("geometry/diagram_object.go", go_header("geometry", _imp_common) + '''// DiagramObject positions one PowerSystemResource within a Diagram.
// CIM: IEC61970 Diagram Layout "DiagramObject".
type DiagramObject struct {
\tcommon.IdentifiedObject
\tDiagram             *Diagram              `json:"diagram,omitempty"`             // optional; CIM: DiagramObject.Diagram -- keine Einheit
\tDiagramObjectStyle  *DiagramObjectStyle   `json:"diagramObjectStyle,omitempty"`  // optional; CIM: DiagramObject.DiagramObjectStyle -- keine Einheit
\tDiagramObjectPoints []*DiagramObjectPoint `json:"diagramObjectPoints,omitempty"` // optional; CIM: DiagramObject.DiagramObjectPoints -- keine Einheit
}
''')

add("geometry/diagram_object_point.go", go_header("geometry", None) + '''// DiagramObjectPoint is one coordinate of a DiagramObject's on-screen
// drawing geometry (distinct from PositionPoint's real-world geometry).
// CIM: IEC61970 Diagram Layout "DiagramObjectPoint".
type DiagramObjectPoint struct {
\tDiagramObject  *DiagramObject `json:"-"`                        // back-reference to owning DiagramObject, excluded from JSON to avoid cycles (see DiagramObject.DiagramObjectPoints)
\tSequenceNumber *int           `json:"sequenceNumber,omitempty"` // optional; CIM: DiagramObjectPoint.sequenceNumber -- keine Einheit
\tXPosition      *float64       `json:"xPosition,omitempty"`      // optional; CIM: DiagramObjectPoint.xPosition -- Einheit: Diagramm-/Bildschirmkoordinate (dimensionslos)
\tYPosition      *float64       `json:"yPosition,omitempty"`      // optional; CIM: DiagramObjectPoint.yPosition -- Einheit: Diagramm-/Bildschirmkoordinate (dimensionslos)
}
''')

add("geometry/diagram_object_style.go", go_header("geometry", _imp_common) + '''// DiagramObjectStyle describes the visual style (color, line style, ...) of
// one or more DiagramObject entries. No attributes beyond the base
// IdentifiedObject fields were verified against our example data.
// CIM: IEC61970 Diagram Layout "DiagramObjectStyle".
type DiagramObjectStyle struct {
\tcommon.IdentifiedObject
}
''')

add("geometry/text_diagram_object.go", go_header("geometry", None) + '''// TextDiagramObject is a DiagramObject variant carrying free text (e.g. a
// label) instead of/in addition to an equipment symbol.
// CIM: IEC61970 Diagram Layout "TextDiagramObject" (extends
// "DiagramObject").
type TextDiagramObject struct {
\tDiagramObject
\tText *string `json:"text,omitempty"` // optional; CIM: TextDiagramObject.text -- keine Einheit
}
''')

# ---------------------------------------------------------------------------
# model/statevariables  (imports: common)
# ---------------------------------------------------------------------------
add("statevariables/sv_power_flow.go", go_header("statevariables", _imp_common) + '''// SvPowerFlow is a state-variable (calculation result) object holding the
// power-flow result (P/Q) at one Terminal, as produced by a CGMES SV
// profile (e.g. from an external load-flow run) -- not a live measurement
// (JAG does not ingest live telemetry, see spec/Konzept.md).
// CIM: CGMES SV profile "SvPowerFlow".
type SvPowerFlow struct {
\tcommon.IdentifiedObject
\tP *float64 `json:"p,omitempty"` // optional; CIM: SvPowerFlow.p -- Einheit: MW; Standard-CIM-Attribut, nicht einzeln gegen unsere Beispieldaten verifiziert
\tQ *float64 `json:"q,omitempty"` // optional; CIM: SvPowerFlow.q -- Einheit: MVAr; Standard-CIM-Attribut, nicht einzeln gegen unsere Beispieldaten verifiziert
}
''')

add("statevariables/sv_voltage.go", go_header("statevariables", _imp_common) + '''// SvVoltage is a state-variable (calculation result) object holding the
// voltage-magnitude/angle result at one TopologicalNode, as produced by a
// CGMES SV profile -- not a live measurement.
// CIM: CGMES SV profile "SvVoltage".
type SvVoltage struct {
\tcommon.IdentifiedObject
\tV     *float64 `json:"v,omitempty"`     // optional; CIM: SvVoltage.v -- Einheit: kV; Standard-CIM-Attribut, nicht einzeln gegen unsere Beispieldaten verifiziert
\tAngle *float64 `json:"angle,omitempty"` // optional; CIM: SvVoltage.angle -- Einheit: Grad; Standard-CIM-Attribut, nicht einzeln gegen unsere Beispieldaten verifiziert
}
''')

print("metadata/geometry/statevariables done, files so far:", len(files))

# ---------------------------------------------------------------------------
# model/control  (Regelung/Netzführung; imports: common, metadata)
# ---------------------------------------------------------------------------
_imp_control = [f"{MOD}/common", f"{MOD}/metadata"]

add("control/power_electronics_connection.go", go_header("control", _imp_control) + '''// PowerElectronicsConnection is the CIM connection-point equipment used
// (in the NSC dialect) to model a "Steuerbox"/EMS-controlled connection
// point (e.g. PV feed-in or a controllable load) -- a JAG Zweipol edge.
// CIM: IEC61970 dynamics "PowerElectronicsConnection" (extends
// "RegulatingCondEq").
type PowerElectronicsConnection struct {
\tcommon.Equipment
\tBaseVoltage                     *metadata.BaseVoltage `json:"baseVoltage,omitempty"`                     // optional; CIM: ConductingEquipment.BaseVoltage -- keine Einheit
\tControllableResourceIdentifier *string                `json:"controllableResourceIdentifier,omitempty"` // optional; CIM: PowerElectronicsConnection.controllableResourceIdentifier -- keine Einheit
\tControlEnabled                  *bool                 `json:"controlEnabled,omitempty"`                  // optional; CIM: RegulatingCondEq.controlEnabled -- keine Einheit
\tRegulatingControl               *RegulatingControl    `json:"regulatingControl,omitempty"`               // optional; CIM: RegulatingCondEq.RegulatingControl -- keine Einheit
}

func (p *PowerElectronicsConnection) IsConductingEquipment() {}

var _ common.ConductingEquipmentRef = (*PowerElectronicsConnection)(nil)
''')

add("control/regulating_control.go", go_header("control", _imp_control) + '''// RegulatingControl describes the steuVA/steuEA control rule attached to a
// PowerElectronicsConnection (e.g. §14a EnWG load control / §9 EEG feed-in
// management): whether it is a discrete or continuous control, whether it
// is enabled, and its allowed value range. CIM: IEC61970 Base
// "RegulatingControl".
type RegulatingControl struct {
\tcommon.IdentifiedObject
\tDiscrete              *bool    `json:"discrete,omitempty"`              // optional; CIM: RegulatingControl.discrete -- keine Einheit
\tEnabled               *bool    `json:"enabled,omitempty"`               // optional; CIM: RegulatingControl.enabled -- keine Einheit
\tMinAllowedTargetValue *float64 `json:"minAllowedTargetValue,omitempty"` // optional; CIM: RegulatingControl.minAllowedTargetValue -- Einheit abhängig vom geregelten Wert (z.B. kV bei Spannungsregelung)
\tMaxAllowedTargetValue *float64 `json:"maxAllowedTargetValue,omitempty"` // optional; CIM: RegulatingControl.maxAllowedTargetValue -- Einheit abhängig vom geregelten Wert (z.B. kV bei Spannungsregelung)
\tTargetValue           *float64 `json:"targetValue,omitempty"`           // optional; CIM: RegulatingControl.targetValue -- Einheit abhängig vom geregelten Wert (z.B. p.u. Spannungssollwert)
\tMode                  *string  `json:"mode,omitempty"`                  // optional; CIM: RegulatingControl.mode -- keine Einheit (z.B. "voltage")
}
''')

add("control/energy_scheduling_type.go", go_header("control", _imp_control) + '''// EnergySchedulingType categorizes a scheduled energy resource (e.g. for a
// PowerElectronicsUnit's planned/steuerbare output). No attributes beyond
// the base IdentifiedObject fields were verified against our example data.
// CIM: IEC61970 Base/Scheduling "EnergySchedulingType" (catalog-like
// classification).
type EnergySchedulingType struct {
\tcommon.IdentifiedObject
}
''')

add("control/time_schedule.go", go_header("control", _imp_control) + '''// TimeSchedule describes a recurring measurement/control schedule (e.g. a
// Meter's reading interval, MeasuringSchedule/TransmissionSchedule).
// CIM: IEC61970 Base "TimeSchedule".
type TimeSchedule struct {
\tcommon.IdentifiedObject
\tDisabled         *bool   `json:"disabled,omitempty"`         // optional; CIM: TimeSchedule.disabled -- keine Einheit
\tRecurrencePeriod *string `json:"recurrencePeriod,omitempty"` // optional; CIM: TimeSchedule.recurrencePeriod -- Einheit: i.d.R. Sekunden (dialektabhängig als Dauer-String)
}
''')

add("control/psr_type.go", go_header("control", _imp_control) + '''// PSRType is a catalog entry classifying a PowerSystemResource (e.g. the
// steuVA/steuEA classification of a controllable connection point).
// CIM: IEC61970 Base "PSRType" (catalog).
type PSRType struct {
\tcommon.IdentifiedObject
}
''')

# ---------------------------------------------------------------------------
# model/limits  (Betriebsmittelgrenzen; imports: common, control)
# ---------------------------------------------------------------------------
_imp_limits = [f"{MOD}/common", f"{MOD}/control"]

add("limits/operational_limit_set.go", go_header("limits", [f"{MOD}/common"]) + '''// OperationalLimitSet groups the operational limits (CurrentLimit,
// VoltageLimit, ...) that apply to one Equipment/Terminal.
// CIM: IEC61970 Base "OperationalLimitSet".
type OperationalLimitSet struct {
\tcommon.IdentifiedObject
}
''')

add("limits/operational_limit_type.go", go_header("limits", [f"{MOD}/common"]) + '''// OperationalLimitType is a catalog entry describing the kind of limit
// (e.g. "patl" = Permanent Admissible Transmission Loading, "tatl" =
// Temporary Admissible Transmission Loading) referenced by CurrentLimit/
// VoltageLimit. Encoded in the raw CIM/CGMES data as an rdf:resource enum
// reference, not a literal string (see pandapower/README.md for how this
// was discovered empirically). CIM: IEC61970 Base "OperationalLimitType"
// (catalog).
type OperationalLimitType struct {
\tcommon.IdentifiedObject
\tKind *string `json:"kind,omitempty"` // optional; CIM: OperationalLimitType.kind -- keine Einheit (Enum-Wert, z.B. "patl"/"tatl")
}
''')

add("limits/current_limit.go", go_header("limits", [f"{MOD}/common"]) + '''// CurrentLimit is a thermal current limit (e.g. the permanent admissible
// transmission loading, "patl") belonging to an OperationalLimitSet.
// CIM: IEC61970 Base "CurrentLimit".
type CurrentLimit struct {
\tcommon.IdentifiedObject
\tOperationalLimitSet  *OperationalLimitSet  `json:"operationalLimitSet,omitempty"`  // optional; CIM: OperationalLimit.OperationalLimitSet -- keine Einheit
\tOperationalLimitType *OperationalLimitType `json:"operationalLimitType,omitempty"` // optional; CIM: OperationalLimit.OperationalLimitType -- keine Einheit
\tValue                *float64              `json:"value,omitempty"`                // optional; CIM: CurrentLimit.value -- Einheit: Ampere (A)
}
''')

add("limits/voltage_limit.go", go_header("limits", [f"{MOD}/common"]) + '''// VoltageLimit is an allowed voltage-magnitude limit belonging to an
// OperationalLimitSet. CIM: IEC61970 Base "VoltageLimit".
type VoltageLimit struct {
\tcommon.IdentifiedObject
\tOperationalLimitSet  *OperationalLimitSet  `json:"operationalLimitSet,omitempty"`  // optional; CIM: OperationalLimit.OperationalLimitSet -- keine Einheit
\tOperationalLimitType *OperationalLimitType `json:"operationalLimitType,omitempty"` // optional; CIM: OperationalLimit.OperationalLimitType -- keine Einheit
\tValue                *float64              `json:"value,omitempty"`                // optional; CIM: VoltageLimit.value -- Einheit: kV
}
''')

add("limits/discrete_control_limit.go", go_header("limits", _imp_limits) + '''// DiscreteControlLimit is one discrete control step (e.g. one steuVA/
// steuEA switching stage) refining a discrete RegulatingControl.
// CIM: IEC61970 Base "DiscreteControlLimit" (NSC-Dialekt-Nutzung).
type DiscreteControlLimit struct {
\tcommon.IdentifiedObject
\tRegulatingControl *control.RegulatingControl `json:"regulatingControl,omitempty"` // optional; CIM: DiscreteControlLimit.RegulatingControl -- keine Einheit
\tSequenceNumber    *int                        `json:"sequenceNumber,omitempty"`    // optional; CIM: DiscreteControlLimit.sequenceNumber -- keine Einheit
\tValue             *float64                    `json:"value,omitempty"`             // optional; CIM: DiscreteControlLimit.value -- Einheit abhängig vom geregelten Wert
}
''')

print("control/limits done, files so far:", len(files))

# ---------------------------------------------------------------------------
# model/switchgear  (Schaltgeräte; imports: common, metadata)
# ---------------------------------------------------------------------------
_imp_switchgear = [f"{MOD}/common", f"{MOD}/metadata"]

add("switchgear/switch.go", go_header("switchgear", _imp_switchgear) + '''// Switch is the generic CIM switching-device base (e.g. a Trenner in the
// NSC dialect). Closed switches are treated as zero-ohm (collapsed) in
// JAG's electrical topology view; open switches interrupt it (see
// spec/Konzept.md Topologie-Kapitel). CIM: IEC61970 Base "Switch" (extends
// "ConductingEquipment").
type Switch struct {
\tcommon.Equipment
\tBaseVoltage  *metadata.BaseVoltage `json:"baseVoltage,omitempty"`  // optional; CIM: ConductingEquipment.BaseVoltage -- keine Einheit
\tNormalOpen   *bool                 `json:"normalOpen,omitempty"`   // optional; CIM: Switch.normalOpen -- keine Einheit
\tOpen         *bool                 `json:"open,omitempty"`         // optional; CIM: Switch.open -- keine Einheit (tatsächlicher, nicht nur normaler Schaltzustand)
\tRetained     *bool                 `json:"retained,omitempty"`     // optional; CIM: Switch.retained -- keine Einheit
\tLocked       *bool                 `json:"locked,omitempty"`       // optional; CIM: Switch.locked -- keine Einheit
\tRatedCurrent *float64              `json:"ratedCurrent,omitempty"` // optional; CIM: Switch.ratedCurrent -- Einheit: Ampere (A)
}

func (s *Switch) IsConductingEquipment() {}

var _ common.ConductingEquipmentRef = (*Switch)(nil)
''')

add("switchgear/breaker.go", go_header("switchgear", None) + '''// Breaker is a circuit breaker (Lasttrennschalter) -- a Switch subtype
// capable of interrupting fault current. No attributes beyond the Switch
// base fields were verified against our example data.
// CIM: IEC61970 Base "Breaker" (extends "ProtectedSwitch"/"Switch").
type Breaker struct {
\tSwitch
}
''')

add("switchgear/disconnector.go", go_header("switchgear", None) + '''// Disconnector is an isolating switch (Trenner) not rated to interrupt
// load/fault current -- zero-ohm when closed, in JAG's electrical topology
// view. No attributes beyond the Switch base fields were verified against
// our example data. CIM: IEC61970 Base "Disconnector" (extends "Switch").
type Disconnector struct {
\tSwitch
}
''')

add("switchgear/fuse.go", go_header("switchgear", None) + '''// Fuse is a fusible cutout (Sicherung) -- zero-ohm when intact/closed, in
// JAG's electrical topology view. Some dialects (NSC) carry the trip
// current as Fuse.nominalCurrent instead of (or in parallel to)
// Switch.ratedCurrent -- both were observed in the same NSC example data.
// CIM: IEC61970 Base "Fuse" (extends "Switch").
type Fuse struct {
\tSwitch
\tNominalCurrent *float64 `json:"nominalCurrent,omitempty"` // optional; CIM: Fuse.nominalCurrent -- Einheit: Ampere (A); NSC-Dialekt-Attributname, parallel zu Switch.RatedCurrent
}
''')

add("statevariables/sv_switch.go", go_header("statevariables", [f"{MOD}/common"]) + '''// SvSwitch is a state-variable (calculation result) object holding the
// computed switch position -- not a live measurement. No attributes beyond
// the base IdentifiedObject fields were verified against our example data
// -- this is a minimal placeholder. CIM: CGMES SV profile "SvSwitch".
type SvSwitch struct {
\tcommon.IdentifiedObject
}
''')

add("statevariables/sv_status.go", go_header("statevariables", [f"{MOD}/common"]) + '''// SvStatus is a state-variable (calculation result) object holding the
// computed in-service status of an equipment -- not a live measurement. No
// attributes beyond the base IdentifiedObject fields were verified against
// our example data -- this is a minimal placeholder.
// CIM: CGMES SV profile "SvStatus".
type SvStatus struct {
\tcommon.IdentifiedObject
}
''')

add("statevariables/sv_tap_step.go", go_header("statevariables", [f"{MOD}/common"]) + '''// SvTapStep is a state-variable (calculation result) object holding the
// currently-computed tap position of a tap changer -- not a live
// measurement. No attributes beyond the base IdentifiedObject fields were
// verified against our example data -- this is a minimal placeholder.
// CIM: CGMES SV profile "SvTapStep".
type SvTapStep struct {
\tcommon.IdentifiedObject
}
''')

add("statevariables/sv_shunt_compensator_sections.go", go_header("statevariables", [f"{MOD}/common"]) + '''// SvShuntCompensatorSections is a state-variable (calculation result)
// object holding the currently-computed connected section count of a
// shunt compensator -- not a live measurement. No attributes beyond the
// base IdentifiedObject fields were verified against our example data --
// this is a minimal placeholder. CIM: CGMES SV profile
// "SvShuntCompensatorSections".
type SvShuntCompensatorSections struct {
\tcommon.IdentifiedObject
}
''')

# ---------------------------------------------------------------------------
# model/lines  (Leitungen/Kabel; imports: common, metadata, limits)
# ---------------------------------------------------------------------------
_imp_lines = [f"{MOD}/common", f"{MOD}/limits", f"{MOD}/metadata"]

add("lines/ac_line_segment.go", go_header("lines", _imp_lines) + '''// ACLineSegment is one cable/overhead-line segment -- a JAG Zweipol edge.
// Several ACLineSegments chained between two topological branch points form
// one logical JAG "ACLine" container (see spec/Konzept.md, ACLine-boundary
// decision). In CGMES, r/x/bch are given directly on the segment; in the
// NSC dialect they are instead looked up via PerLengthImpedance (see
// PerLengthSequenceImpedance). CIM: IEC61970 Base "ACLineSegment" (extends
// "Conductor").
type ACLineSegment struct {
\tcommon.Equipment
\tBaseVoltage                *metadata.BaseVoltage       `json:"baseVoltage,omitempty"`                // optional; CIM: ConductingEquipment.BaseVoltage -- keine Einheit
\tOperationalLimitSet        *limits.OperationalLimitSet `json:"operationalLimitSet,omitempty"`        // optional; CIM: Equipment.OperationalLimitSet -- keine Einheit
\tLength                     *float64                    `json:"length,omitempty"`                     // optional; CIM: Conductor.length -- Einheit: km
\tR                          *float64                    `json:"r,omitempty"`                          // optional; CIM: ACLineSegment.r -- Einheit: Ohm (Mitsystem)
\tX                          *float64                    `json:"x,omitempty"`                          // optional; CIM: ACLineSegment.x -- Einheit: Ohm (Mitsystem)
\tR0                         *float64                    `json:"r0,omitempty"`                         // optional; CIM: ACLineSegment.r0 -- Einheit: Ohm (Nullsystem)
\tX0                         *float64                    `json:"x0,omitempty"`                         // optional; CIM: ACLineSegment.x0 -- Einheit: Ohm (Nullsystem)
\tGch                        *float64                    `json:"gch,omitempty"`                        // optional; CIM: ACLineSegment.gch -- Einheit: Siemens (Mitsystem)
\tBch                        *float64                    `json:"bch,omitempty"`                        // optional; CIM: ACLineSegment.bch -- Einheit: Siemens (Mitsystem)
\tG0ch                       *float64                    `json:"g0ch,omitempty"`                       // optional; CIM: ACLineSegment.g0ch -- Einheit: Siemens (Nullsystem)
\tB0ch                       *float64                    `json:"b0ch,omitempty"`                       // optional; CIM: ACLineSegment.b0ch -- Einheit: Siemens (Nullsystem)
\tShortCircuitEndTemperature *float64                    `json:"shortCircuitEndTemperature,omitempty"` // optional; CIM: ACLineSegment.shortCircuitEndTemperature -- Einheit: °C
\tPerLengthImpedance         *PerLengthSequenceImpedance `json:"perLengthImpedance,omitempty"`         // optional; CIM: ACLineSegment.PerLengthImpedance -- keine Einheit; NSC-Dialekt: Katalog-Nachschlagewert statt direkter r/x-Angabe oben
}

func (a *ACLineSegment) IsConductingEquipment() {}

var _ common.ConductingEquipmentRef = (*ACLineSegment)(nil)
''')

add("lines/line.go", go_header("lines", [f"{MOD}/common"]) + '''// Line is a CIM EquipmentContainer grouping the ACLineSegments (and any
// intermediate Junction splices) of one cable/overhead-line route outside a
// station -- corresponds to JAG's own "acline" container type (see
// spec/Konzept.md). CIM: IEC61970 Base "Line" (extends
// "EquipmentContainer", "ConnectivityNodeContainer").
type Line struct {
\tcommon.IdentifiedObject
}

func (l *Line) IsEquipmentContainer()        {}
func (l *Line) IsConnectivityNodeContainer() {}

var (
\t_ common.EquipmentContainer           = (*Line)(nil)
\t_ common.ConnectivityNodeContainer    = (*Line)(nil)
)
''')

add("lines/junction.go", go_header("lines", [f"{MOD}/common"]) + '''// Junction is a cable joint/splice ("Muffe") connecting two ACLineSegments
// -- a JAG Zweipol edge in its own right. A plain 2-port Junction
// (Durchgangsmuffe) does not end a JAG ACLine; only a branching
// Junction (Abzweig-/T-Muffe, 3+ connections) is a real topological
// branch point (see spec/Konzept.md, ACLine-boundary decision). No
// attributes beyond the base Equipment fields were verified against our
// example data. CIM: IEC61970 Base "Junction" (extends
// "ConductingEquipment").
type Junction struct {
\tcommon.Equipment
}

func (j *Junction) IsConductingEquipment() {}

var _ common.ConductingEquipmentRef = (*Junction)(nil)
''')

add("lines/per_length_sequence_impedance.go", go_header("lines", [f"{MOD}/common"]) + '''// PerLengthSequenceImpedance is a catalog entry describing per-kilometer
// impedance values for a cable/line type, referenced by ACLineSegment
// instead of (or in addition to) direct total r/x values -- the NSC-dialect
// pattern (see spec/Idee.md). CIM: IEC61970 Base
// "PerLengthSequenceImpedance" (catalog, extends "PerLengthImpedance").
type PerLengthSequenceImpedance struct {
\tcommon.IdentifiedObject
\tR  *float64 `json:"r,omitempty"`  // optional; CIM: PerLengthSequenceImpedance.r -- Einheit: Ohm/km (Mitsystem)
\tR0 *float64 `json:"r0,omitempty"` // optional; CIM: PerLengthSequenceImpedance.r0 -- Einheit: Ohm/km (Nullsystem)
\tX  *float64 `json:"x,omitempty"`  // optional; CIM: PerLengthSequenceImpedance.x -- Einheit: Ohm/km (Mitsystem)
\tX0 *float64 `json:"x0,omitempty"` // optional; CIM: PerLengthSequenceImpedance.x0 -- Einheit: Ohm/km (Nullsystem)
}
''')

print("switchgear/lines done, files so far:", len(files))

# ---------------------------------------------------------------------------
# model/busbarsandnodes  (Sammelschienen/Knoten; imports: common, metadata, limits, statevariables)
# ---------------------------------------------------------------------------
_imp_bb = [f"{MOD}/common", f"{MOD}/limits", f"{MOD}/metadata", f"{MOD}/statevariables"]

add("busbarsandnodes/busbar_section.go", go_header("busbarsandnodes", [f"{MOD}/common", f"{MOD}/metadata"]) + '''// BusbarSection is a physical busbar segment. Despite being modeled as CIM
// Equipment, JAG exposes it as a Node (real connection point), not a
// Zweipol Edge -- see spec/Idee.md Gruppe 4 "Sammelschienen/Knoten" and
// spec/Konzept.md's Busbar-Container decision.
// CIM: IEC61970 Base "BusbarSection" (extends "Connector").
type BusbarSection struct {
\tcommon.Equipment
\tBaseVoltage *metadata.BaseVoltage `json:"baseVoltage,omitempty"` // optional; CIM: ConductingEquipment.BaseVoltage -- keine Einheit
\tIpMax       *float64              `json:"ipMax,omitempty"`       // optional; CIM: BusbarSection.ipMax -- Einheit: kA (max. zulässiger Stoßkurzschlussstrom)
}

func (b *BusbarSection) IsConductingEquipment() {}

var _ common.ConductingEquipmentRef = (*BusbarSection)(nil)
''')

add("busbarsandnodes/connectivity_node.go", go_header("busbarsandnodes", [f"{MOD}/common"]) + '''// ConnectivityNode is the physical connection point shared by the
// Terminals of the equipment attached to it, before any zero-ohm/switch
// reduction is applied. CIM: IEC61970 Base "ConnectivityNode".
type ConnectivityNode struct {
\tcommon.IdentifiedObject
\tConnectivityNodeContainer common.ConnectivityNodeContainer `json:"connectivityNodeContainer,omitempty"` // optional; CIM: ConnectivityNode.ConnectivityNodeContainer -- keine Einheit
\tTopologicalNode           *TopologicalNode                 `json:"topologicalNode,omitempty"`           // optional; CIM: ConnectivityNode.TopologicalNode -- keine Einheit; Zuordnung nach Nullohm-Reduktion, siehe spec/Konzept.md
}
''')

add("busbarsandnodes/topological_node.go", go_header("busbarsandnodes", [f"{MOD}/common", f"{MOD}/metadata", f"{MOD}/statevariables"]) + '''// TopologicalNode is the electrical connection point AFTER zero-ohm/switch
// reduction (CGMES TP profile) -- several ConnectivityNode objects may
// collapse onto one TopologicalNode via closed Breaker/Disconnector/Fuse
// elements between them. JAG's own ReliCapGrid_Espheim extraction uses
// TopologicalNode objects directly as pandapower buses (see
// pandapower/README.md). CIM: IEC61970 Topology "TopologicalNode".
type TopologicalNode struct {
\tcommon.IdentifiedObject
\tBaseVoltage               *metadata.BaseVoltage             `json:"baseVoltage,omitempty"`               // optional; CIM: TopologicalNode.BaseVoltage -- keine Einheit
\tConnectivityNodeContainer common.ConnectivityNodeContainer  `json:"connectivityNodeContainer,omitempty"` // optional; CIM: TopologicalNode.ConnectivityNodeContainer -- keine Einheit
\tSvVoltage                 *statevariables.SvVoltage         `json:"svVoltage,omitempty"`                 // optional; CIM: TopologicalNode.SvVoltage -- keine Einheit; Lastfluss-Rechenergebnis, keine Live-Messung
\tTerminals                 []*Terminal                       `json:"-"`                                   // back-reference list, excluded from JSON to avoid cycles (see Terminal.TopologicalNode)
}
''')

add("busbarsandnodes/bus_name_marker.go", go_header("busbarsandnodes", [f"{MOD}/common"]) + '''// BusNameMarker groups Terminals that should share one displayed busbar
// name/label, independent of the actual ConnectivityNode/TopologicalNode
// topology. No attributes beyond the base IdentifiedObject fields were
// verified against our example data. CIM: IEC61970 Base "BusNameMarker".
type BusNameMarker struct {
\tcommon.IdentifiedObject
}
''')

add("busbarsandnodes/terminal.go", go_header("busbarsandnodes", _imp_bb) + '''// Terminal is a CIM connection point of one piece of ConductingEquipment.
// JAG deliberately avoids modeling Terminals explicitly wherever possible
// (each JAG Edge just directly references its two connections instead) --
// this struct exists for lossless CIM import/export round-tripping.
// ACDCTerminal.sequenceNumber (1 or 2) maps directly onto JAG's own
// convention: 1 = toward the higher voltage level / toward the
// transformer, 2 = toward ground/earth potential (see spec/Idee.md).
// CIM: IEC61970 Base "Terminal" (extends "ACDCTerminal").
type Terminal struct {
\tcommon.IdentifiedObject
\tConductingEquipment common.ConductingEquipmentRef `json:"conductingEquipment,omitempty"` // optional; CIM: Terminal.ConductingEquipment -- keine Einheit
\tConnectivityNode    *ConnectivityNode             `json:"connectivityNode,omitempty"`    // optional; CIM: Terminal.ConnectivityNode -- keine Einheit
\tTopologicalNode     *TopologicalNode              `json:"topologicalNode,omitempty"`     // optional; CIM: Terminal.TopologicalNode -- keine Einheit
\tSvPowerFlow         *statevariables.SvPowerFlow   `json:"svPowerFlow,omitempty"`         // optional; CIM: Terminal.SvPowerFlow -- keine Einheit; Lastfluss-Rechenergebnis, keine Live-Messung
\tSequenceNumber      *int                          `json:"sequenceNumber,omitempty"`      // optional; CIM: ACDCTerminal.sequenceNumber -- keine Einheit; 1 oder 2, siehe JAG-Terminal-Konvention oben
\tConnected           *bool                         `json:"connected,omitempty"`           // optional; CIM: ACDCTerminal.connected -- keine Einheit
\tBusNameMarker       *BusNameMarker                `json:"busNameMarker,omitempty"`       // optional; CIM: ACDCTerminal.BusNameMarker -- keine Einheit
\tOperationalLimitSet *limits.OperationalLimitSet   `json:"operationalLimitSet,omitempty"` // optional; CIM: ACDCTerminal.OperationalLimitSet -- keine Einheit
\tPhases              *string                       `json:"phases,omitempty"`              // optional; CIM: Terminal.phases -- keine Einheit (in Testdaten immer "ABC", da JAG 1-phasig vereinfacht)
}
''')

# ---------------------------------------------------------------------------
# model/transformers  (imports: common, metadata, limits, busbarsandnodes)
# ---------------------------------------------------------------------------
_imp_trafo = [f"{MOD}/busbarsandnodes", f"{MOD}/common", f"{MOD}/limits", f"{MOD}/metadata"]

add("transformers/power_transformer.go", go_header("transformers", [f"{MOD}/common", f"{MOD}/limits"]) + '''// PowerTransformer is a (2-winding) transformer -- modeled in JAG as a
// single ordinary Zweipol edge connecting its HV (OS) node directly to its
// LV (US) node, NOT as a four-terminal element with a virtual star-point
// node (see spec/Konzept.md, Transformer-Entscheidung). Multi-winding
// (>2 voltage level) transformers are explicitly unsupported by JAG (hard
// import failure). CIM: IEC61970 Base "PowerTransformer" (extends
// "ConductingEquipment", "EquipmentContainer").
type PowerTransformer struct {
\tcommon.Equipment
\tOperationalLimitSet                    *limits.OperationalLimitSet `json:"operationalLimitSet,omitempty"`                    // optional; CIM: Equipment.OperationalLimitSet -- keine Einheit
\tEnds                                   []*PowerTransformerEnd      `json:"ends,omitempty"`                                   // optional; CIM: PowerTransformer.PowerTransformerEnd -- keine Einheit; die (üblicherweise 2) Wicklungsenden
\tIsPartOfGeneratorUnit                  *bool                       `json:"isPartOfGeneratorUnit,omitempty"`                  // optional; CIM: PowerTransformer.isPartOfGeneratorUnit -- keine Einheit
\tOperationalValuesConsidered            *bool                       `json:"operationalValuesConsidered,omitempty"`            // optional; CIM: PowerTransformer.operationalValuesConsidered -- keine Einheit
\tBeforeShCircuitHighestOperatingCurrent *float64                    `json:"beforeShCircuitHighestOperatingCurrent,omitempty"` // optional; CIM: PowerTransformer.beforeShCircuitHighestOperatingCurrent -- Einheit: Ampere (A)
\tBeforeShCircuitHighestOperatingVoltage *float64                    `json:"beforeShCircuitHighestOperatingVoltage,omitempty"` // optional; CIM: PowerTransformer.beforeShCircuitHighestOperatingVoltage -- Einheit: kV
\tBeforeShortCircuitAnglePf              *float64                    `json:"beforeShortCircuitAnglePf,omitempty"`              // optional; CIM: PowerTransformer.beforeShortCircuitAnglePf -- Einheit: Grad
\tHighSideMinOperatingU                  *float64                    `json:"highSideMinOperatingU,omitempty"`                  // optional; CIM: PowerTransformer.highSideMinOperatingU -- Einheit: kV
}

func (t *PowerTransformer) IsConductingEquipment() {}
func (t *PowerTransformer) IsEquipmentContainer()  {}

var (
\t_ common.ConductingEquipmentRef = (*PowerTransformer)(nil)
\t_ common.EquipmentContainer     = (*PowerTransformer)(nil)
)
''')

add("transformers/power_transformer_end.go", go_header("transformers", _imp_trafo) + '''// PowerTransformerEnd is one winding side (OS or US) of a PowerTransformer,
// carrying that side's rated values and short-circuit impedance. JAG maps
// TransformerEnd.endNumber (1/2) directly onto its own Terminal 1/2
// convention (1 = HV/OS side, 2 = LV/US side). Side-specific attributes are
// kept as two separate Sachdaten groups on the same JAG edge, not as
// separate virtual nodes (see spec/Konzept.md).
// CIM: IEC61970 Base "PowerTransformerEnd" (extends "TransformerEnd").
type PowerTransformerEnd struct {
\tcommon.IdentifiedObject
\tPowerTransformer       *PowerTransformer         `json:"-"`                                // back-reference to the owning transformer, excluded from JSON to avoid cycles (see PowerTransformer.Ends)
\tEndNumber              *int                      `json:"endNumber,omitempty"`              // optional; CIM: TransformerEnd.endNumber -- keine Einheit; 1=OS/HV, 2=US/LV
\tTerminal               *busbarsandnodes.Terminal `json:"terminal,omitempty"`               // optional; CIM: TransformerEnd.Terminal -- keine Einheit
\tBaseVoltage            *metadata.BaseVoltage     `json:"baseVoltage,omitempty"`            // optional; CIM: TransformerEnd.BaseVoltage -- keine Einheit
\tGrounded               *bool                     `json:"grounded,omitempty"`               // optional; CIM: TransformerEnd.grounded -- keine Einheit
\tRground                *float64                  `json:"rground,omitempty"`                // optional; CIM: TransformerEnd.rground -- Einheit: Ohm; nur relevant wenn Grounded=true
\tXground                *float64                  `json:"xground,omitempty"`                // optional; CIM: TransformerEnd.xground -- Einheit: Ohm; nur relevant wenn Grounded=true
\tRatedU                 *float64                  `json:"ratedU,omitempty"`                 // optional; CIM: PowerTransformerEnd.ratedU -- Einheit: kV
\tRatedS                 *float64                  `json:"ratedS,omitempty"`                 // optional; CIM: PowerTransformerEnd.ratedS -- Einheit: MVA
\tR                      *float64                  `json:"r,omitempty"`                      // optional; CIM: PowerTransformerEnd.r -- Einheit: Ohm (Mitsystem)
\tX                      *float64                  `json:"x,omitempty"`                      // optional; CIM: PowerTransformerEnd.x -- Einheit: Ohm (Mitsystem)
\tR0                     *float64                  `json:"r0,omitempty"`                     // optional; CIM: PowerTransformerEnd.r0 -- Einheit: Ohm (Nullsystem)
\tX0                     *float64                  `json:"x0,omitempty"`                     // optional; CIM: PowerTransformerEnd.x0 -- Einheit: Ohm (Nullsystem)
\tG                      *float64                  `json:"g,omitempty"`                      // optional; CIM: PowerTransformerEnd.g -- Einheit: Siemens (Mitsystem)
\tB                      *float64                  `json:"b,omitempty"`                      // optional; CIM: PowerTransformerEnd.b -- Einheit: Siemens (Mitsystem)
\tG0                     *float64                  `json:"g0,omitempty"`                     // optional; CIM: PowerTransformerEnd.g0 -- Einheit: Siemens (Nullsystem)
\tB0                     *float64                  `json:"b0,omitempty"`                     // optional; CIM: PowerTransformerEnd.b0 -- Einheit: Siemens (Nullsystem)
\tConnectionKind         *string                   `json:"connectionKind,omitempty"`         // optional; CIM: PowerTransformerEnd.connectionKind -- keine Einheit; Schaltgruppe (Y/D/Z)
\tPhaseAngleClock        *int                      `json:"phaseAngleClock,omitempty"`        // optional; CIM: PowerTransformerEnd.phaseAngleClock -- keine Einheit; Schaltgruppen-Uhrzeigerzahl
\tMaxApparentPowerFactor *float64                  `json:"maxApparentPowerFactor,omitempty"` // optional; CIM: PowerTransformerEnd.maxApparentPowerFactor -- keine Einheit; NSC-Dialekt
}
''')

_tap_files = [
("ratio_tap_changer.go", "RatioTapChanger", '''// RatioTapChanger models a transformer end's tap changer for voltage
// magnitude regulation (Anhängsel of PowerTransformerEnd). No attributes
// beyond the control reference were verified against our example data --
// this is a minimal placeholder. CIM: IEC61970 Base "RatioTapChanger"
// (extends "TapChanger").
type RatioTapChanger struct {
\tcommon.IdentifiedObject
\tTapChangerControl *TapChangerControl `json:"tapChangerControl,omitempty"` // optional; CIM: TapChanger.TapChangerControl -- keine Einheit
}
'''),
("tap_changer_control.go", "TapChangerControl", '''// TapChangerControl describes the regulation target (e.g. target voltage)
// of a RatioTapChanger/PhaseTapChanger. No attributes beyond the base
// IdentifiedObject fields were verified against our example data -- this
// is a minimal placeholder. CIM: IEC61970 Base "TapChangerControl"
// (extends "RegulatingControl").
type TapChangerControl struct {
\tcommon.IdentifiedObject
}
'''),
("phase_tap_changer_linear.go", "PhaseTapChangerLinear", '''// PhaseTapChangerLinear models a phase-shifting tap changer with a linear
// angle/step relationship. No attributes beyond the base IdentifiedObject
// fields were verified against our example data -- this is a minimal
// placeholder. CIM: IEC61970 Base "PhaseTapChangerLinear" (extends
// "PhaseTapChanger").
type PhaseTapChangerLinear struct {
\tcommon.IdentifiedObject
}
'''),
("phase_tap_changer_asymmetrical.go", "PhaseTapChangerAsymmetrical", '''// PhaseTapChangerAsymmetrical models a phase-shifting tap changer with an
// asymmetrical winding connection. No attributes beyond the base
// IdentifiedObject fields were verified against our example data -- this
// is a minimal placeholder. CIM: IEC61970 Base
// "PhaseTapChangerAsymmetrical" (extends "PhaseTapChangerNonLinear").
type PhaseTapChangerAsymmetrical struct {
\tcommon.IdentifiedObject
}
'''),
("phase_tap_changer_symmetrical.go", "PhaseTapChangerSymmetrical", '''// PhaseTapChangerSymmetrical models a phase-shifting tap changer with a
// symmetrical winding connection. No attributes beyond the base
// IdentifiedObject fields were verified against our example data -- this
// is a minimal placeholder. CIM: IEC61970 Base "PhaseTapChangerSymmetrical"
// (extends "PhaseTapChangerNonLinear").
type PhaseTapChangerSymmetrical struct {
\tcommon.IdentifiedObject
}
'''),
]
for fname, sname, body in _tap_files:
    add(f"transformers/{fname}", go_header("transformers", [f"{MOD}/common"]) + body)

add("transformers/phase_tap_changer_tabular.go", go_header("transformers", [f"{MOD}/common"]) + '''// PhaseTapChangerTabular models a phase-shifting tap changer whose
// angle/ratio per step is given by a lookup table
// (PhaseTapChangerTable/PhaseTapChangerTablePoint) rather than a formula.
// No attributes beyond the table reference were verified against our
// example data. CIM: IEC61970 Base "PhaseTapChangerTabular" (extends
// "PhaseTapChanger").
type PhaseTapChangerTabular struct {
\tcommon.IdentifiedObject
\tPhaseTapChangerTable *PhaseTapChangerTable `json:"phaseTapChangerTable,omitempty"` // optional; CIM: PhaseTapChangerTabular.PhaseTapChangerTable -- keine Einheit
}
''')

add("transformers/ratio_tap_changer_table.go", go_header("transformers", [f"{MOD}/common"]) + '''// RatioTapChangerTable is a catalog entry: a lookup table of
// RatioTapChangerTablePoint rows for a tabular ratio tap changer. No
// attributes beyond the point list were verified against our example data
// -- this is a minimal placeholder. CIM: IEC61970 Base
// "RatioTapChangerTable" (catalog).
type RatioTapChangerTable struct {
\tcommon.IdentifiedObject
\tPoints []*RatioTapChangerTablePoint `json:"points,omitempty"` // optional; CIM: RatioTapChangerTable.RatioTapChangerTablePoint -- keine Einheit
}
''')

add("transformers/ratio_tap_changer_table_point.go", go_header("transformers", None) + '''// RatioTapChangerTablePoint is one row (one tap step) of a
// RatioTapChangerTable. No attributes beyond the step number were
// verified against our example data -- this is a minimal placeholder.
// CIM: IEC61970 Base "RatioTapChangerTablePoint" (catalog).
type RatioTapChangerTablePoint struct {
\tStep *int `json:"step,omitempty"` // optional; CIM: TapChangerTablePoint.step -- keine Einheit
}
''')

add("transformers/phase_tap_changer_table.go", go_header("transformers", [f"{MOD}/common"]) + '''// PhaseTapChangerTable is a catalog entry: a lookup table of
// PhaseTapChangerTablePoint rows for a tabular phase tap changer. No
// attributes beyond the point list were verified against our example data
// -- this is a minimal placeholder. CIM: IEC61970 Base
// "PhaseTapChangerTable" (catalog).
type PhaseTapChangerTable struct {
\tcommon.IdentifiedObject
\tPoints []*PhaseTapChangerTablePoint `json:"points,omitempty"` // optional; CIM: PhaseTapChangerTable.PhaseTapChangerTablePoint -- keine Einheit
}
''')

add("transformers/phase_tap_changer_table_point.go", go_header("transformers", None) + '''// PhaseTapChangerTablePoint is one row (one tap step) of a
// PhaseTapChangerTable. No attributes beyond the step number were verified
// against our example data -- this is a minimal placeholder.
// CIM: IEC61970 Base "PhaseTapChangerTablePoint" (catalog).
type PhaseTapChangerTablePoint struct {
\tStep *int `json:"step,omitempty"` // optional; CIM: TapChangerTablePoint.step -- keine Einheit
}
''')

print("busbarsandnodes/transformers done, files so far:", len(files))

# ---------------------------------------------------------------------------
# model/hierarchy  (Gruppierung/Hierarchie; imports: common, metadata, geometry, busbarsandnodes)
# ---------------------------------------------------------------------------
add("hierarchy/geographical_region.go", go_header("hierarchy", [f"{MOD}/common"]) + '''// GeographicalRegion is the top-level geographic grouping (e.g. a country
// or utility service area) -- a loose grouping, not part of JAG's own
// Container tree (see spec/Konzept.md's Netzregion decision, which notes
// GeographicalRegion/SubGeographicalRegion are likewise outside CIM's own
// ConnectivityNodeContainer tree). CIM: IEC61970 Base "GeographicalRegion".
type GeographicalRegion struct {
\tcommon.IdentifiedObject
}
''')

add("hierarchy/sub_geographical_region.go", go_header("hierarchy", [f"{MOD}/common"]) + '''// SubGeographicalRegion is a sub-division of a GeographicalRegion (e.g. a
// utility's regional service area), the direct parent of Substation in
// CIM's own hierarchy. CIM: IEC61970 Base "SubGeographicalRegion".
type SubGeographicalRegion struct {
\tcommon.IdentifiedObject
\tRegion *GeographicalRegion `json:"region,omitempty"` // optional; CIM: SubGeographicalRegion.Region -- keine Einheit
}
''')

add("hierarchy/substation.go", go_header("hierarchy", [f"{MOD}/common"]) + '''// Substation is a station (umbrella term covering Substation proper,
// Umschaltwerk, Mittelspannungsschaltanlage, Ortsnetzstation in JAG's own
// terminology -- distinguished only via a Sachdaten key, not a separate
// container type, see spec/Konzept.md). Holds one or more VoltageLevels.
// CIM: IEC61970 Base "Substation" (extends "EquipmentContainer").
type Substation struct {
\tcommon.IdentifiedObject
\tRegion        *SubGeographicalRegion `json:"region,omitempty"`        // optional; CIM: Substation.Region -- keine Einheit
\tVoltageLevels []*VoltageLevel        `json:"voltageLevels,omitempty"` // optional; CIM: Substation.VoltageLevels -- keine Einheit
}

func (s *Substation) IsEquipmentContainer() {}

var _ common.EquipmentContainer = (*Substation)(nil)
''')

add("hierarchy/voltage_level.go", go_header("hierarchy", [f"{MOD}/busbarsandnodes", f"{MOD}/common", f"{MOD}/metadata"]) + '''// VoltageLevel groups the equipment of a Substation operating at one
// voltage level, containing Bay(s) and/or a busbar structure.
// CIM: IEC61970 Base "VoltageLevel" (extends "EquipmentContainer",
// "ConnectivityNodeContainer").
type VoltageLevel struct {
\tcommon.IdentifiedObject
\tSubstation       *Substation                        `json:"substation,omitempty"`       // optional; CIM: VoltageLevel.Substation -- keine Einheit
\tBaseVoltage      *metadata.BaseVoltage              `json:"baseVoltage,omitempty"`       // optional; CIM: VoltageLevel.BaseVoltage -- keine Einheit
\tHighVoltageLimit *float64                           `json:"highVoltageLimit,omitempty"` // optional; CIM: VoltageLevel.highVoltageLimit -- Einheit: kV
\tLowVoltageLimit  *float64                           `json:"lowVoltageLimit,omitempty"`  // optional; CIM: VoltageLevel.lowVoltageLimit -- Einheit: kV
\tBays             []*Bay                             `json:"bays,omitempty"`             // optional; CIM: VoltageLevel.Bays -- keine Einheit
\tTopologicalNodes []*busbarsandnodes.TopologicalNode `json:"-"`                          // back-reference list, excluded from JSON to avoid cycles (see TopologicalNode.ConnectivityNodeContainer)
}

func (v *VoltageLevel) IsEquipmentContainer()        {}
func (v *VoltageLevel) IsConnectivityNodeContainer() {}

var (
\t_ common.EquipmentContainer        = (*VoltageLevel)(nil)
\t_ common.ConnectivityNodeContainer = (*VoltageLevel)(nil)
)
''')

add("hierarchy/bay.go", go_header("hierarchy", [f"{MOD}/common"]) + '''// Bay groups the equipment of one feeder/field ("Feld") within a
// VoltageLevel -- role, not type: Abgangsfeld (outgoing) vs.
// Einspeisefeld/incoming feeder (see spec/Idee.md terminology table).
// CIM: IEC61970 Base "Bay" (extends "EquipmentContainer",
// "ConnectivityNodeContainer").
type Bay struct {
\tcommon.IdentifiedObject
\tVoltageLevel *VoltageLevel `json:"voltageLevel,omitempty"` // optional; CIM: Bay.VoltageLevel -- keine Einheit
}

func (b *Bay) IsEquipmentContainer()        {}
func (b *Bay) IsConnectivityNodeContainer() {}

var (
\t_ common.EquipmentContainer        = (*Bay)(nil)
\t_ common.ConnectivityNodeContainer = (*Bay)(nil)
)
''')

add("hierarchy/feeder.go", go_header("hierarchy", [f"{MOD}/common"]) + '''// Feeder is the low-voltage synonym role for Bay (see spec/Idee.md
// terminology table: "Feld -> Bay (CIM) / Feeder (Niederspannungs-Synonym)").
// No attributes beyond the base IdentifiedObject fields were verified
// against our example data -- this is a minimal placeholder.
// CIM: IEC61970 Base "Feeder" (extends "EquipmentContainer").
type Feeder struct {
\tcommon.IdentifiedObject
}

func (f *Feeder) IsEquipmentContainer() {}

var _ common.EquipmentContainer = (*Feeder)(nil)
''')

add("hierarchy/generic_equipment.go", go_header("hierarchy", [f"{MOD}/common"]) + '''// GenericEquipment is a catch-all CIM equipment class used where no more
// specific class applies. No attributes beyond the base Equipment fields
// were verified against our example data -- this is a minimal placeholder.
// CIM: IEC61970 Base "GenericEquipment" (extends "ConductingEquipment").
type GenericEquipment struct {
\tcommon.Equipment
}

func (g *GenericEquipment) IsConductingEquipment() {}

var _ common.ConductingEquipmentRef = (*GenericEquipment)(nil)
''')

add("hierarchy/house.go", go_header("hierarchy", [f"{MOD}/common", f"{MOD}/geometry"]) + '''// House is JAG's renamed form of the CIM class "Building" (see
// spec/Idee.md JAG-terminology convention: the underlying CIM class name
// remains "Building"; JAG uses "House" at the Go level only). Represents a
// house-connection's physical building. CIM: IEC61970 Base "Building".
type House struct {
\tcommon.IdentifiedObject
\tLocation *geometry.Location `json:"location,omitempty"` // optional; CIM: Building (PowerSystemResource).Location -- keine Einheit
}
''')

add("hierarchy/usage_point_location.go", go_header("hierarchy", [f"{MOD}/common", f"{MOD}/geometry"]) + '''// UsagePointLocation is the location of a house-connection/usage point
// (NSC dialect). CIM: IEC61968 Metering "UsagePointLocation" (extends
// "Location").
type UsagePointLocation struct {
\tcommon.IdentifiedObject
\tMainAddress *string            `json:"mainAddress,omitempty"` // optional; CIM: UsagePointLocation.mainAddress -- keine Einheit
\tPositions   []*geometry.PositionPoint `json:"positions,omitempty"` // optional; CIM: Location.PositionPoints -- keine Einheit
}
''')

add("hierarchy/usage_point.go", go_header("hierarchy", [f"{MOD}/common"]) + '''// UsagePoint is a house-connection / metering point (NSC dialect) --
// the point at which a Producer/Consumer/Prosumer connects to the grid.
// CIM: IEC61968 Metering "UsagePoint".
type UsagePoint struct {
\tcommon.IdentifiedObject
\tUsagePointLocation *UsagePointLocation `json:"usagePointLocation,omitempty"` // optional; CIM: UsagePoint.UsagePointLocation -- keine Einheit
\tIsVirtual          *bool               `json:"isVirtual,omitempty"`          // optional; CIM: UsagePoint.isVirtual -- keine Einheit
\tIsSdp              *bool               `json:"isSdp,omitempty"`              // optional; CIM: UsagePoint.isSdp -- keine Einheit (Supply Delivery Point)
\tOutageRegion       *string             `json:"outageRegion,omitempty"`       // optional; CIM: UsagePoint.outageRegion -- keine Einheit
\tPhaseCode          *string             `json:"phaseCode,omitempty"`          // optional; CIM: UsagePoint.phaseCode -- keine Einheit
	RatedPower         *float64            `json:"ratedPower,omitempty"`         // optional; CIM: UsagePoint.ratedPower -- Einheit: kW
}
''')

print("hierarchy done, files so far:", len(files))

# ---------------------------------------------------------------------------
# model/connectionusers  (Erzeuger/Verbraucher; imports: common, metadata, control)
# ---------------------------------------------------------------------------
_imp_cu = [f"{MOD}/common", f"{MOD}/control", f"{MOD}/metadata"]

add("connectionusers/synchronous_machine.go", go_header("connectionusers", [f"{MOD}/common", f"{MOD}/metadata"]) + '''// SynchronousMachine is a rotating generator/motor (e.g. a conventional
// power-plant generator) connected to the grid. CIM: IEC61970 Base
// "SynchronousMachine" (extends "RotatingMachine").
type SynchronousMachine struct {
\tcommon.Equipment
\tBaseVoltage  *metadata.BaseVoltage `json:"baseVoltage,omitempty"`  // optional; CIM: ConductingEquipment.BaseVoltage -- keine Einheit
\tRatedS       *float64              `json:"ratedS,omitempty"`       // optional; CIM: RotatingMachine.ratedS -- Einheit: MVA
\tRatedU       *float64              `json:"ratedU,omitempty"`       // optional; CIM: RotatingMachine.ratedU -- Einheit: kV
\tP            *float64              `json:"p,omitempty"`            // optional; CIM: RotatingMachine.p -- Einheit: MW
\tQ            *float64              `json:"q,omitempty"`            // optional; CIM: RotatingMachine.q -- Einheit: MVAr
\tGeneratingUnit *GeneratingUnit     `json:"generatingUnit,omitempty"` // optional; CIM: SynchronousMachine.GeneratingUnit -- keine Einheit
}

func (s *SynchronousMachine) IsConductingEquipment() {}

var _ common.ConductingEquipmentRef = (*SynchronousMachine)(nil)
''')

add("connectionusers/asynchronous_machine.go", go_header("connectionusers", [f"{MOD}/common"]) + '''// AsynchronousMachine is an induction generator/motor. No attributes
// beyond the base Equipment fields were verified against our example data
// -- this is a minimal placeholder. CIM: IEC61970 Base
// "AsynchronousMachine" (extends "RotatingMachine").
type AsynchronousMachine struct {
\tcommon.Equipment
}

func (a *AsynchronousMachine) IsConductingEquipment() {}

var _ common.ConductingEquipmentRef = (*AsynchronousMachine)(nil)
''')

add("connectionusers/generating_unit.go", go_header("connectionusers", [f"{MOD}/common"]) + '''// GeneratingUnit is the non-electrical "plant" side of a generator
// (fuel/prime-mover aggregate), referenced by SynchronousMachine.
// CIM: IEC61970 Base "GeneratingUnit" (extends "Equipment").
type GeneratingUnit struct {
\tcommon.Equipment
\tMaxOperatingP *float64 `json:"maxOperatingP,omitempty"` // optional; CIM: GeneratingUnit.maxOperatingP -- Einheit: MW
\tMinOperatingP *float64 `json:"minOperatingP,omitempty"` // optional; CIM: GeneratingUnit.minOperatingP -- Einheit: MW
\tRatedNetMaxP  *float64 `json:"ratedNetMaxP,omitempty"`  // optional; CIM: GeneratingUnit.ratedNetMaxP -- Einheit: MW
}

func (g *GeneratingUnit) IsConductingEquipment() {}

var _ common.ConductingEquipmentRef = (*GeneratingUnit)(nil)
''')

add("connectionusers/thermal_generating_unit.go", go_header("connectionusers", None) + '''// ThermalGeneratingUnit is a fossil/thermal-fired GeneratingUnit
// specialization. No attributes beyond the base fields were verified
// against our example data -- this is a minimal placeholder.
// CIM: IEC61970 Base "ThermalGeneratingUnit" (extends "GeneratingUnit").
type ThermalGeneratingUnit struct {
\tGeneratingUnit
}
''')

add("connectionusers/hydro_generating_unit.go", go_header("connectionusers", None) + '''// HydroGeneratingUnit is a hydroelectric GeneratingUnit specialization. No
// attributes beyond the base fields were verified against our example data
// -- this is a minimal placeholder. CIM: IEC61970 Base
// "HydroGeneratingUnit" (extends "GeneratingUnit").
type HydroGeneratingUnit struct {
\tGeneratingUnit
}
''')

add("connectionusers/wind_generating_unit.go", go_header("connectionusers", None) + '''// WindGeneratingUnit is a wind-turbine GeneratingUnit specialization. No
// attributes beyond the base fields were verified against our example data
// -- this is a minimal placeholder. CIM: IEC61970 Base
// "WindGeneratingUnit" (extends "GeneratingUnit").
type WindGeneratingUnit struct {
\tGeneratingUnit
}
''')

add("connectionusers/solar_generating_unit.go", go_header("connectionusers", None) + '''// SolarGeneratingUnit is a photovoltaic GeneratingUnit specialization. No
// attributes beyond the base fields were verified against our example data
// -- this is a minimal placeholder. CIM: IEC61970 Base
// "SolarGeneratingUnit" (extends "GeneratingUnit").
type SolarGeneratingUnit struct {
\tGeneratingUnit
}
''')

add("connectionusers/fossil_fuel.go", go_header("connectionusers", [f"{MOD}/common"]) + '''// FossilFuel describes one fuel type used by a ThermalGeneratingUnit. No
// attributes beyond the base IdentifiedObject fields were verified against
// our example data -- this is a minimal placeholder. CIM: IEC61970 Base
// "FossilFuel".
type FossilFuel struct {
\tcommon.IdentifiedObject
\tThermalGeneratingUnit *ThermalGeneratingUnit `json:"thermalGeneratingUnit,omitempty"` // optional; CIM: FossilFuel.ThermalGeneratingUnit -- keine Einheit
}
''')

add("connectionusers/reactive_capability_curve.go", go_header("connectionusers", [f"{MOD}/common"]) + '''// ReactiveCapabilityCurve describes a SynchronousMachine's P/Qmin/Qmax
// operating envelope as a set of CurveData points. No attributes beyond
// the point list were verified against our example data -- this is a
// minimal placeholder. CIM: IEC61970 Base "ReactiveCapabilityCurve"
// (extends "Curve").
type ReactiveCapabilityCurve struct {
\tcommon.IdentifiedObject
\tCurveData []*CurveData `json:"curveData,omitempty"` // optional; CIM: Curve.CurveDatas -- keine Einheit
}
''')

add("connectionusers/curve_data.go", go_header("connectionusers", None) + '''// CurveData is one (x, y1, y2) point of a ReactiveCapabilityCurve (or
// other CIM Curve). CIM: IEC61970 Base "CurveData".
type CurveData struct {
\tXvalue *float64 `json:"xvalue,omitempty"` // optional; CIM: CurveData.xvalue -- Einheit: MW (P)
\tY1value *float64 `json:"y1value,omitempty"` // optional; CIM: CurveData.y1value -- Einheit: MVAr (Qmin)
\tY2value *float64 `json:"y2value,omitempty"` // optional; CIM: CurveData.y2value -- Einheit: MVAr (Qmax)
}
''')

add("connectionusers/photo_voltaic_unit.go", go_header("connectionusers", [f"{MOD}/common", f"{MOD}/control"]) + '''// PhotoVoltaicUnit is a PV generation unit behind a
// PowerElectronicsConnection (NSC dialect) -- a pure Producer role.
// CIM: IEC61970 Base "PhotoVoltaicUnit" (extends "PowerElectronicsUnit").
type PhotoVoltaicUnit struct {
\tcommon.IdentifiedObject
\tPowerElectronicsConnection *control.PowerElectronicsConnection `json:"powerElectronicsConnection,omitempty"` // optional; CIM: PowerElectronicsUnit.PowerElectronicsConnection -- keine Einheit
\tMaxP                       *float64                             `json:"maxP,omitempty"`                       // optional; CIM: PowerElectronicsUnit.maxP -- Einheit: kW
\tMinP                       *float64                             `json:"minP,omitempty"`                       // optional; CIM: PowerElectronicsUnit.minP -- Einheit: kW
}
''')

add("connectionusers/battery_unit.go", go_header("connectionusers", [f"{MOD}/common", f"{MOD}/control"]) + '''// BatteryUnit is a battery storage unit behind a
// PowerElectronicsConnection (NSC dialect) -- a Prosumer role (can both
// absorb and inject power). CIM: IEC61970 Base "BatteryUnit" (extends
// "PowerElectronicsUnit").
type BatteryUnit struct {
\tcommon.IdentifiedObject
\tPowerElectronicsConnection *control.PowerElectronicsConnection `json:"powerElectronicsConnection,omitempty"` // optional; CIM: PowerElectronicsUnit.PowerElectronicsConnection -- keine Einheit
\tRatedE                     *float64                             `json:"ratedE,omitempty"`                     // optional; CIM: BatteryUnit.ratedE -- Einheit: kWh
\tStoredE                    *float64                             `json:"storedE,omitempty"`                    // optional; CIM: BatteryUnit.storedE -- Einheit: kWh
}
''')

add("connectionusers/heatpump.go", go_header("connectionusers", [f"{MOD}/common", f"{MOD}/control"]) + '''// Heatpump is a controllable consumption device (steuVA, §14a EnWG) behind
// a PowerElectronicsConnection/EnergyConsumer (NSC dialect) -- a Consumer
// role. No further attributes beyond the connection reference were
// verified against our example data. CIM: not a standard IEC61970 class --
// NSC-dialect-specific equipment type.
type Heatpump struct {
\tcommon.IdentifiedObject
\tPowerElectronicsConnection *control.PowerElectronicsConnection `json:"powerElectronicsConnection,omitempty"` // optional; NSC-Dialekt -- keine Einheit
}
''')

add("connectionusers/air_conditioning_unit.go", go_header("connectionusers", [f"{MOD}/common", f"{MOD}/control"]) + '''// AirConditioningUnit is a controllable consumption device (steuVA, §14a
// EnWG), analogous to Heatpump -- a Consumer role. No further attributes
// beyond the connection reference were verified against our example data.
// CIM: not a standard IEC61970 class -- NSC-dialect-specific equipment type.
type AirConditioningUnit struct {
\tcommon.IdentifiedObject
\tPowerElectronicsConnection *control.PowerElectronicsConnection `json:"powerElectronicsConnection,omitempty"` // optional; NSC-Dialekt -- keine Einheit
}
''')

add("connectionusers/external_network_injection.go", go_header("connectionusers", [f"{MOD}/common"]) + '''// ExternalNetworkInjection represents the equivalent feed-in from the
// upstream grid at a model boundary (e.g. the HV/MV interface in a
// bus-branch CGMES model). CIM: IEC61970 Base
// "ExternalNetworkInjection" (extends "RegulatingCondEq").
type ExternalNetworkInjection struct {
\tcommon.Equipment
\tGovernorSCD *float64 `json:"governorSCD,omitempty"` // optional; CIM: ExternalNetworkInjection.governorSCD -- Einheit: MW/Hz
\tP           *float64 `json:"p,omitempty"`           // optional; CIM: ExternalNetworkInjection.p -- Einheit: MW
\tQ           *float64 `json:"q,omitempty"`           // optional; CIM: ExternalNetworkInjection.q -- Einheit: MVAr
}

func (e *ExternalNetworkInjection) IsConductingEquipment() {}

var _ common.ConductingEquipmentRef = (*ExternalNetworkInjection)(nil)
''')

add("connectionusers/equivalent_injection.go", go_header("connectionusers", [f"{MOD}/common"]) + '''// EquivalentInjection is a simplified equivalent source/sink used to
// represent an aggregated part of the grid not otherwise modeled (e.g. in
// bus-branch CGMES test configurations). CIM: IEC61970 Base
// "EquivalentInjection" (extends "ConductingEquipment").
type EquivalentInjection struct {
\tcommon.Equipment
\tP    *float64 `json:"p,omitempty"`    // optional; CIM: EquivalentInjection.p -- Einheit: MW
\tQ    *float64 `json:"q,omitempty"`    // optional; CIM: EquivalentInjection.q -- Einheit: MVAr
\tMinP *float64 `json:"minP,omitempty"` // optional; CIM: EquivalentInjection.minP -- Einheit: MW
\tMaxP *float64 `json:"maxP,omitempty"` // optional; CIM: EquivalentInjection.maxP -- Einheit: MW
\tMinQ *float64 `json:"minQ,omitempty"` // optional; CIM: EquivalentInjection.minQ -- Einheit: MVAr
\tMaxQ *float64 `json:"maxQ,omitempty"` // optional; CIM: EquivalentInjection.maxQ -- Einheit: MVAr
}

func (e *EquivalentInjection) IsConductingEquipment() {}

var _ common.ConductingEquipmentRef = (*EquivalentInjection)(nil)
''')

add("connectionusers/energy_consumer.go", go_header("connectionusers", [f"{MOD}/common", f"{MOD}/metadata"]) + '''// EnergyConsumer is a generic load (Auspeiser/Consumer role) drawing
// energy from the grid at a connection point. CIM: IEC61970 Base
// "EnergyConsumer" (extends "ConductingEquipment").
type EnergyConsumer struct {
\tcommon.Equipment
\tBaseVoltage *metadata.BaseVoltage `json:"baseVoltage,omitempty"` // optional; CIM: ConductingEquipment.BaseVoltage -- keine Einheit
\tP           *float64              `json:"p,omitempty"`           // optional; CIM: EnergyConsumer.p -- Einheit: MW
\tQ           *float64              `json:"q,omitempty"`           // optional; CIM: EnergyConsumer.q -- Einheit: MVAr
\tPhaseConnection *string           `json:"phaseConnection,omitempty"` // optional; CIM: EnergyConsumer.phaseConnection -- keine Einheit
}

func (e *EnergyConsumer) IsConductingEquipment() {}

var _ common.ConductingEquipmentRef = (*EnergyConsumer)(nil)
''')

add("connectionusers/conform_load.go", go_header("connectionusers", [f"{MOD}/common"]) + '''// ConformLoad is an EnergyConsumer whose demand follows one of a
// utility's standard load profiles/curves (as opposed to a NonConformLoad
// with its own individual profile). CIM: IEC61970 Base "ConformLoad"
// (extends "EnergyConsumer").
type ConformLoad struct {
\tEnergyConsumer
\tLoadResponse *LoadResponseCharacteristic `json:"loadResponse,omitempty"` // optional; CIM: EnergyConsumer.LoadResponse -- keine Einheit
}
''')

add("connectionusers/load_response_characteristic.go", go_header("connectionusers", [f"{MOD}/common"]) + '''// LoadResponseCharacteristic describes how a load's active/reactive power
// varies with voltage (ZIP-model exponents), used for voltage-dependent
// load-flow calculation. CIM: IEC61970 Base "LoadResponseCharacteristic".
type LoadResponseCharacteristic struct {
\tcommon.IdentifiedObject
\tExponentModel  *bool    `json:"exponentModel,omitempty"`  // optional; CIM: LoadResponseCharacteristic.exponentModel -- keine Einheit
\tPVoltageExponent *float64 `json:"pVoltageExponent,omitempty"` // optional; CIM: LoadResponseCharacteristic.pVoltageExponent -- keine Einheit
\tQVoltageExponent *float64 `json:"qVoltageExponent,omitempty"` // optional; CIM: LoadResponseCharacteristic.qVoltageExponent -- keine Einheit
\tPConstantPower *float64 `json:"pConstantPower,omitempty"` // optional; CIM: LoadResponseCharacteristic.pConstantPower -- keine Einheit (Anteil 0..1)
\tPConstantCurrent *float64 `json:"pConstantCurrent,omitempty"` // optional; CIM: LoadResponseCharacteristic.pConstantCurrent -- keine Einheit (Anteil 0..1)
\tPConstantImpedance *float64 `json:"pConstantImpedance,omitempty"` // optional; CIM: LoadResponseCharacteristic.pConstantImpedance -- keine Einheit (Anteil 0..1)
}
''')

print("connectionusers done, files so far:", len(files))

# ---------------------------------------------------------------------------
# model/compensation  (imports: common, metadata)
# ---------------------------------------------------------------------------
add("compensation/linear_shunt_compensator.go", go_header("compensation", [f"{MOD}/common", f"{MOD}/metadata"]) + '''// LinearShuntCompensator is a shunt capacitor/reactor bank whose
// admittance per section is constant (linear) -- a Zweipol edge to ground.
// CIM: IEC61970 Base "LinearShuntCompensator" (extends
// "ShuntCompensator").
type LinearShuntCompensator struct {
\tcommon.Equipment
\tBaseVoltage      *metadata.BaseVoltage `json:"baseVoltage,omitempty"`      // optional; CIM: ConductingEquipment.BaseVoltage -- keine Einheit
\tBPerSection      *float64              `json:"bPerSection,omitempty"`      // optional; CIM: LinearShuntCompensator.bPerSection -- Einheit: Siemens
\tGPerSection      *float64              `json:"gPerSection,omitempty"`      // optional; CIM: LinearShuntCompensator.gPerSection -- Einheit: Siemens
\tMaximumSections  *int                  `json:"maximumSections,omitempty"`  // optional; CIM: ShuntCompensator.maximumSections -- keine Einheit
\tNormalSections   *int                  `json:"normalSections,omitempty"`   // optional; CIM: ShuntCompensator.normalSections -- keine Einheit
}

func (l *LinearShuntCompensator) IsConductingEquipment() {}

var _ common.ConductingEquipmentRef = (*LinearShuntCompensator)(nil)
''')

add("compensation/nonlinear_shunt_compensator.go", go_header("compensation", [f"{MOD}/common", f"{MOD}/metadata"]) + '''// NonlinearShuntCompensator is a shunt capacitor/reactor bank whose
// admittance per section is given by a NonlinearShuntCompensatorPoint
// lookup table instead of a constant value. CIM: IEC61970 Base
// "NonlinearShuntCompensator" (extends "ShuntCompensator").
type NonlinearShuntCompensator struct {
\tcommon.Equipment
\tBaseVoltage *metadata.BaseVoltage              `json:"baseVoltage,omitempty"` // optional; CIM: ConductingEquipment.BaseVoltage -- keine Einheit
\tPoints      []*NonlinearShuntCompensatorPoint  `json:"points,omitempty"`      // optional; CIM: NonlinearShuntCompensator.NonlinearShuntCompensatorPoints -- keine Einheit
}

func (n *NonlinearShuntCompensator) IsConductingEquipment() {}

var _ common.ConductingEquipmentRef = (*NonlinearShuntCompensator)(nil)
''')

add("compensation/nonlinear_shunt_compensator_point.go", go_header("compensation", None) + '''// NonlinearShuntCompensatorPoint is one row (one section count -> b/g
// value) of a NonlinearShuntCompensator's lookup table.
// CIM: IEC61970 Base "NonlinearShuntCompensatorPoint".
type NonlinearShuntCompensatorPoint struct {
\tSectionNumber *int     `json:"sectionNumber,omitempty"` // optional; CIM: NonlinearShuntCompensatorPoint.sectionNumber -- keine Einheit
\tB             *float64 `json:"b,omitempty"`             // optional; CIM: NonlinearShuntCompensatorPoint.b -- Einheit: Siemens
\tG             *float64 `json:"g,omitempty"`             // optional; CIM: NonlinearShuntCompensatorPoint.g -- Einheit: Siemens
}
''')

add("compensation/static_var_compensator.go", go_header("compensation", [f"{MOD}/common"]) + '''// StaticVarCompensator (SVC) is a power-electronic reactive-power
// compensation device. No attributes beyond the base Equipment fields were
// verified against our example data -- this is a minimal placeholder.
// CIM: IEC61970 Base "StaticVarCompensator" (extends
// "RegulatingCondEq").
type StaticVarCompensator struct {
\tcommon.Equipment
}

func (s *StaticVarCompensator) IsConductingEquipment() {}

var _ common.ConductingEquipmentRef = (*StaticVarCompensator)(nil)
''')

add("compensation/series_compensator.go", go_header("compensation", [f"{MOD}/common"]) + '''// SeriesCompensator is a series capacitor/reactor -- a Zweipol edge with
// fixed impedance in series with a line. No attributes beyond the base
// Equipment fields were verified against our example data -- this is a
// minimal placeholder. CIM: IEC61970 Base "SeriesCompensator" (extends
// "ConductingEquipment").
type SeriesCompensator struct {
\tcommon.Equipment
}

func (s *SeriesCompensator) IsConductingEquipment() {}

var _ common.ConductingEquipmentRef = (*SeriesCompensator)(nil)
''')

print("compensation done, files so far:", len(files))

# ---------------------------------------------------------------------------
# model/measurement  (imports: common, metadata, hierarchy, control)
# ---------------------------------------------------------------------------
add("measurement/meter.go", go_header("measurement", [f"{MOD}/common", f"{MOD}/control", f"{MOD}/hierarchy"]) + '''// Meter is a physical metering device (Messung) at a UsagePoint, possibly
// linked to a TimeSchedule for time-of-use tariffs (NSC dialect).
// CIM: IEC61968 Metering "Meter" (extends "EndDevice").
type Meter struct {
\tcommon.IdentifiedObject
\tUsagePoint   *hierarchy.UsagePoint  `json:"usagePoint,omitempty"`   // optional; CIM: EndDevice.UsagePoints -- keine Einheit
\tTimeSchedule *control.TimeSchedule  `json:"timeSchedule,omitempty"` // optional; NSC-Dialekt -- keine Einheit
\tSerialNumber *string                `json:"serialNumber,omitempty"` // optional; CIM: Asset.serialNumber (via EndDevice) -- keine Einheit
}
''')

add("measurement/analog.go", go_header("measurement", [f"{MOD}/common"]) + '''// Analog is a measurement point definition (metadata describing what is
// measured, e.g. "active power at Terminal X") -- not the value itself
// (see AnalogValue). No attributes beyond the base fields were verified
// against our example data -- this is a minimal placeholder.
// CIM: IEC61970 Meas "Analog" (extends "Measurement").
type Analog struct {
\tcommon.IdentifiedObject
}
''')

add("measurement/analog_value.go", go_header("measurement", [f"{MOD}/common"]) + '''// AnalogValue holds one measured value for an Analog measurement point.
// Per spec/Idee.md, JAG does not currently ingest live measurement values
// -- this struct exists for lossless CIM import/export round-tripping
// only. CIM: IEC61970 Meas "AnalogValue" (extends "MeasurementValue").
type AnalogValue struct {
\tcommon.IdentifiedObject
\tAnalog *Analog  `json:"analog,omitempty"` // optional; CIM: AnalogValue.Analog -- keine Einheit
\tValue  *float64 `json:"value,omitempty"`  // optional; CIM: AnalogValue.value -- Einheit: abhängig vom gemessenen Analog (siehe Analog.unitSymbol)
}
''')

add("measurement/remote_source.go", go_header("measurement", [f"{MOD}/common"]) + '''// RemoteSource links a Measurement to the RemoteUnit/telemetry channel it
// arrives on. No attributes beyond the base IdentifiedObject fields were
// verified against our example data -- this is a minimal placeholder.
// CIM: IEC61970 SCADA "RemoteSource" (extends "RemotePoint").
type RemoteSource struct {
\tcommon.IdentifiedObject
\tRemoteUnit *RemoteUnit `json:"remoteUnit,omitempty"` // optional; CIM: RemotePoint.RemoteUnit -- keine Einheit
}
''')

add("measurement/remote_unit.go", go_header("measurement", [f"{MOD}/common"]) + '''// RemoteUnit is a telemetry/SCADA remote terminal unit (RTU). No
// attributes beyond the base IdentifiedObject fields were verified against
// our example data -- this is a minimal placeholder.
// CIM: IEC61970 SCADA "RemoteUnit".
type RemoteUnit struct {
\tcommon.IdentifiedObject
\tCommunicationLink *CommunicationLink `json:"communicationLink,omitempty"` // optional; CIM: RemoteUnit.CommunicationLinks -- keine Einheit
}
''')

add("measurement/communication_link.go", go_header("measurement", [f"{MOD}/common"]) + '''// CommunicationLink is the SCADA communication channel connecting one or
// more RemoteUnits. No attributes beyond the base IdentifiedObject fields
// were verified against our example data -- this is a minimal placeholder.
// CIM: IEC61970 SCADA "CommunicationLink".
type CommunicationLink struct {
\tcommon.IdentifiedObject
}
''')

print("measurement done, files so far:", len(files))

# ---------------------------------------------------------------------------
# model/grouping  (imports: common, connectionusers, busbarsandnodes)
# ---------------------------------------------------------------------------
add("grouping/topological_island.go", go_header("grouping", [f"{MOD}/busbarsandnodes", f"{MOD}/common"]) + '''// TopologicalIsland groups all TopologicalNodes that are part of the same
// energized, connected island in a power-flow solution (CGMES SV profile).
// No attributes beyond the node list were verified against our example
// data. CIM: CGMES SV profile "TopologicalIsland".
type TopologicalIsland struct {
\tcommon.IdentifiedObject
\tTopologicalNodes []*busbarsandnodes.TopologicalNode `json:"topologicalNodes,omitempty"` // optional; CIM: TopologicalIsland.TopologicalNodes -- keine Einheit
}
''')

add("grouping/conform_load_group.go", go_header("grouping", [f"{MOD}/common", f"{MOD}/connectionusers"]) + '''// ConformLoadGroup groups ConformLoad objects that share the same standard
// load-profile scaling within a LoadArea/SubLoadArea. No attributes beyond
// the member list were verified against our example data -- this is a
// minimal placeholder. CIM: IEC61970 Base "ConformLoadGroup" (extends
// "LoadGroup").
type ConformLoadGroup struct {
\tcommon.IdentifiedObject
\tEnergyConsumers []*connectionusers.ConformLoad `json:"energyConsumers,omitempty"` // optional; CIM: ConformLoadGroup.EnergyConsumers -- keine Einheit
}
''')

add("grouping/load_area.go", go_header("grouping", [f"{MOD}/common"]) + '''// LoadArea is a top-level geographic grouping of load-forecast areas. No
// attributes beyond the base IdentifiedObject fields were verified against
// our example data -- this is a minimal placeholder. CIM: IEC61970 Base
// "LoadArea".
type LoadArea struct {
\tcommon.IdentifiedObject
}
''')

add("grouping/sub_load_area.go", go_header("grouping", [f"{MOD}/common"]) + '''// SubLoadArea is a sub-division of a LoadArea, the direct parent of
// ConformLoadGroup in CIM's own hierarchy. No attributes beyond the base
// IdentifiedObject fields were verified against our example data -- this
// is a minimal placeholder. CIM: IEC61970 Base "SubLoadArea".
type SubLoadArea struct {
\tcommon.IdentifiedObject
\tLoadArea *LoadArea `json:"loadArea,omitempty"` // optional; CIM: SubLoadArea.LoadArea -- keine Einheit
}
''')

add("grouping/control_area.go", go_header("grouping", [f"{MOD}/common"]) + '''// ControlArea is a balancing/scheduling area used for tie-flow and AGC
// accounting (e.g. one utility's control zone). CIM: IEC61970 Base
// "ControlArea".
type ControlArea struct {
\tcommon.IdentifiedObject
\tNetInterchange   *float64                        `json:"netInterchange,omitempty"`   // optional; CIM: ControlArea.netInterchange -- Einheit: MW
\tGeneratingUnits  []*ControlAreaGeneratingUnit    `json:"generatingUnits,omitempty"`  // optional; CIM: ControlArea.ControlAreaGeneratingUnit -- keine Einheit
\tTieFlows         []*TieFlow                      `json:"tieFlows,omitempty"`         // optional; CIM: ControlArea.TieFlow -- keine Einheit
}
''')

add("grouping/control_area_generating_unit.go", go_header("grouping", [f"{MOD}/common", f"{MOD}/connectionusers"]) + '''// ControlAreaGeneratingUnit is the association-class linking a
// ControlArea to one of its member GeneratingUnits. No attributes beyond
// the two references were verified against our example data.
// CIM: IEC61970 Base "ControlAreaGeneratingUnit".
type ControlAreaGeneratingUnit struct {
\tcommon.IdentifiedObject
\tControlArea     *ControlArea                        `json:"controlArea,omitempty"`     // optional; CIM: ControlAreaGeneratingUnit.ControlArea -- keine Einheit
\tGeneratingUnit  *connectionusers.GeneratingUnit      `json:"generatingUnit,omitempty"`  // optional; CIM: ControlAreaGeneratingUnit.GeneratingUnit -- keine Einheit
}
''')

add("grouping/tie_flow.go", go_header("grouping", [f"{MOD}/busbarsandnodes", f"{MOD}/common"]) + '''// TieFlow is the association-class linking a ControlArea to a boundary
// Terminal whose power flow counts toward that area's net interchange.
// CIM: IEC61970 Base "TieFlow".
type TieFlow struct {
\tcommon.IdentifiedObject
\tControlArea *ControlArea              `json:"controlArea,omitempty"` // optional; CIM: TieFlow.ControlArea -- keine Einheit
\tTerminal    *busbarsandnodes.Terminal `json:"terminal,omitempty"`    // optional; CIM: TieFlow.Terminal -- keine Einheit
\tPositive    *bool                     `json:"positive,omitempty"`    // optional; CIM: TieFlow.positiveFlowIn -- keine Einheit
}
''')

print("grouping done, files so far:", len(files))

# ---------------------------------------------------------------------------
# write everything to disk
# ---------------------------------------------------------------------------
for rel_path, content in files.items():
    full_path = os.path.join(ROOT, rel_path)
    os.makedirs(os.path.dirname(full_path), exist_ok=True)
    with open(full_path, "w", encoding="utf-8", newline="\n") as f:
        f.write(content)

print("wrote", len(files), "files to", ROOT)
