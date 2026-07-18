package common

import coremodel "gitlab.com/openk-nsc/jag/internal/core/model"

// Reserved Sachdaten keys for the human-readable name/label of any
// Equipment or Container. These used to be dedicated struct fields on
// core/model.Equipment/Container; per the "generic core, semantics via
// Sachdaten" simplification they now flow through the ordinary Attribute
// mechanism instead of being special-cased in the core data structures —
// one generic data channel for descriptive data instead of two.
const (
	AttributeKeyName  coremodel.AttributeKey = "name"
	AttributeKeyLabel coremodel.AttributeKey = "label"

	// AttributeKeyClass holds an Equipment's raw CIM class (e.g. "Breaker",
	// "EnergyConsumer") as an ordinary Sachdaten attribute (added
	// 2026-07-16, HJSON Fachmodell exporter): core/model.Equipment
	// deliberately has no Class field (see its doc comment — Node/Edge
	// role, not CIM subclass, is what the persisted model cares about),
	// but the Fachmodell HJSON exporter needs to reconstruct a `class:`
	// entry per Equipment to write valid Fachmodell files, so
	// sachdaten.go's collectAttributesBatch additionally emits this one
	// attribute for the walk's own root Equipment (never for satellites).
	AttributeKeyClass coremodel.AttributeKey = "cim_class"

	// AttributeKeySatelliteClass holds the raw CIM class of a folded
	// satellite object (e.g. "Wallbox", "PhotoVoltaicUnit",
	// "RegulatingControl", "DiscreteControlLimit", "PowerTransformerEnd") —
	// added 2026-07-19 after a user report that a Wallbox satellite folded
	// into its owning PowerElectronicsConnection's Sachdaten (via the
	// PowerElectronicsUnit.PowerElectronicsConnection exception in
	// collectAttributesBatch) was otherwise only identifiable by its name
	// (e.g. "STEU-24"), with no attribute revealing that it actually is a
	// Wallbox. Unlike AttributeKeyClass (root-Equipment-only, one value),
	// this key is emitted once per folded satellite and is therefore
	// commonly multi-valued (e.g. a PowerElectronicsConnection with a
	// RegulatingControl, a Wallbox, and several DiscreteControlLimit
	// satellites gets 6 values here) — the HJSON exporter's
	// buildAttributes already renders multi-value keys as arrays, so no
	// further exporter/importer change is needed to make this visible.
	AttributeKeySatelliteClass coremodel.AttributeKey = "satellite_cim_class"
)
