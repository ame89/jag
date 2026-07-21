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

	// AttributeKeySatellite holds one folded satellite object's own class
	// AND its own literal attributes bundled together as a single JSON
	// object value: {"class": "Wallbox", "attributes": {"IdentifiedObject.name":
	// "STEU-24", ...}}. Added 2026-07-19, replacing an earlier, simpler
	// "satellite_cim_class"-only key (single scalar per satellite) after a
	// user report that a Wallbox satellite folded into its owning
	// PowerElectronicsConnection's Sachdaten (via the
	// PowerElectronicsUnit.PowerElectronicsConnection exception in
	// collectAttributesBatch) was only identifiable by its name, with no
	// way to reliably tell which name belonged to which satellite class:
	// a bare "satellite_cim_class" array and the shared "IdentifiedObject.
	// name" array (also holding the root's own name) were only positionally
	// correlated by coincidence of walk order, breaking silently the
	// moment any satellite lacked a value for some shared key. Bundling
	// each satellite's own data into one self-contained object value
	// removes the need for any such cross-array correlation. Like the
	// former key, this one is emitted once per folded satellite and is
	// therefore commonly multi-valued (one row per satellite, same
	// OwnerID+Key, see coremodel.Attribute's doc comment) — the HJSON
	// exporter's buildAttributes decodes these into a dedicated
	// Equipment/Segment/BusbarSectionEntry.Satellites list instead of
	// exposing them as a plain Sachdaten attribute.
	AttributeKeySatellite coremodel.AttributeKey = "satellite"

	// AttributeKeyBusbarNode records, for a BusbarSection Equipment ID,
	// the canonical electrical Node ID it was merged into by
	// MergeBusbarSectionNodes (see busbarmerge.go) — i.e. the very same
	// coremodel.Node ID that Edge.Terminal1NodeID/Terminal2NodeID use for
	// every OTHER piece of Equipment actually wired to that busbar. Added
	// 2026-07-21: the persisted model (model_node/model_edge) otherwise
	// has no way to look this up again once BuildNodesAndEdges runs,
	// since a BusbarSection's own raw ConnectivityNode ID is deliberately
	// remapped away and never appears as a Node on its own (see
	// busbarmerge.go's package doc comment) — internal/exporter/hjson2's
	// buildBusbarSections previously had to *guess* a station's busbar
	// node via a "2+ independent branches converge here" heuristic, which
	// was found to misidentify plain series pass-through points as
	// spurious busbars whenever a station's busbar-adjacent disconnectors
	// hang directly off the station's own root container rather than a
	// dedicated Bay (observed in examples/cgmes/ReliCapGrid_Espheim,
	// giving 303 instead of 48 Circuits after an hjson2 export/reimport
	// round-trip). This key removes the guesswork: it's written once per
	// BusbarSection Equipment ID during Pass A (ProcessStationBatch, using
	// that station's own already-computed stMergedResolved), and the
	// exporter reads it back directly instead of re-deriving anything.
	// Emitted only for genuine BusbarSection Equipment (never for
	// ordinary Equipment), so buildAttributes/writeAttributesBlock must
	// skip it exactly like AttributeKeySatellite — it is bookkeeping, not
	// a Sachdaten value a user should ever see in an exported file.
	AttributeKeyBusbarNode coremodel.AttributeKey = "busbar_node_id"
)
