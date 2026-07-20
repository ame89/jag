// Package hjson implements the fourth Phase-1 import dialect: the
// hand-authorable "Fachmodell" HJSON format documented in Konzept.md
// ("HJSON Fachmodell"/"Neue Idee (2026-07-16)"). Unlike CIM/CGMES/NSC (a
// flat RDF/XML object list), this dialect is a hierarchically nested,
// directory-based representation matching how a Netzbetreiber employee
// thinks about the network (a Substation has Busbars and Bays, a Bay has
// Equipment, an ACLine has Segments, a House has Equipment) — see
// Konzept.md for the full worked examples this package implements.
//
// This package only defines the pure file-content shape (types.go) and
// parsing (parse.go) plus directory-layout classification (toplevel.go).
// The two-pass, per-Netzregion resolution into dialect-neutral
// model.StagingRecord values (ID prefixing, connects -> synthetic
// Terminal/ConnectivityNode translation, unit conversion) lives in
// resolve.go. The Phase 1 entrypoint that ties this to a staging.Store
// (mirroring phase1.RunCGMESFiles/RunNSCFiles) lives in
// internal/importer/phase1.
//
// IMPORTANT (hjson-go/v4 parsing limitation, discovered while building this
// package — not obvious from the library's docs): hjson-go/v4 reliably
// parses only *multi-line* HJSON object/array syntax. Dense single-line
// forms like `{ id: SW1, class: Breaker, connects: [N1, N2] }` written
// entirely on one line can fail to parse ("Found ']' where a key name was
// expected"). Hand-authored Fachmodell files (and everything this
// package's exporter counterpart writes) must therefore always use
// multi-line object/array syntax.
package hjson

// File is the parsed shape of one Fachmodell .hjson file. Depending on
// which top-level directory the file lives under (see toplevel.go), only a
// subset of these fields is populated by a well-formed file:
//   - ONS/KVS ("Substation"/"distribution-box" top-level containers):
//     Busbars + Bays, plus optional container-level Attributes
//     (e.g. station_kind/region Sachdaten).
//   - Kabel ("acline" top-level containers): Segments only.
//   - Haushalte ("house" top-level containers): Equipment, plus optional
//     container-level Attributes (e.g. MaLo/MeLo).
//
// No "id" field exists at the top level — the container's own ID always
// comes from the filename (see Konzept.md: "Da die Datei bereits über
// ihren Pfad eindeutig identifiziert ist, entfällt ein zusätzliches
// id-Feld im Dateiinhalt selbst"), and is therefore always global.
//
// ID scoping convention (2026-07-20 revision): every OTHER id/connects/
// from/to value appearing anywhere in a file is either local or global,
// distinguished purely by a leading "@": a name starting with "@" is
// local — only unique within this one file — and expands to
// "<this file's container ID>-<name without the leading @>" (e.g. "@6"
// inside O-5.hjson becomes "O-5-6"); a name NOT starting with "@" is
// already a global ID, valid verbatim inside and outside the file (e.g.
// "ABC" stays "ABC" no matter which file it appears in). See
// internal/importer/hjson2/resolve.go's localIDPrefix/resolveID for the
// import-side implementation, and internal/exporter/hjson2/build.go's
// shortenID for the export-side counterpart (an ID exported with a
// "<rootID>-" prefix is shortened AND marked local with "@"; anything
// else — including most Kabel/ACLine segment and node IDs, since those
// rarely happen to share a station-root prefix in raw CIM/CGMES/NSC data —
// is left as a global ID unchanged).
type File struct {
	Busbars    []Busbar               `json:"busbars"`
	Bays       []Bay                  `json:"bays"`
	Segments   []Segment              `json:"segments"`
	Equipment  []Equipment            `json:"equipments"`
	Attributes map[string]interface{} `json:"attributes"`
	Geometry   *GeometryPoint         `json:"geometry"`
}

// GeometryPoint is a 2D WGS84 coordinate (see Konzept.md's "Geometrie"
// section — no height/depth) composed onto a top-level container
// (Substation/KVS/House/ACLine's own Location, not any individual piece of
// Equipment inside it — see internal/impl/common/geometry.go's
// GeometryOwnerContainer). Added 2026-07-19: a container's own Geometry
// was previously computed (BuildGeometry, if the raw source data has a
// PowerSystemResource.Location) but had no representation at all in the
// Fachmodell HJSON format, so it was silently dropped on export.
type GeometryPoint struct {
	Lat float64 `json:"lat"`
	Lon float64 `json:"lon"`
}

// Busbar is one named busbar within a Substation/KVS file, grouping the
// Sections that connecting Equipment is wired to. Every Section of one
// Busbar represents the SAME real electrical node (confirmed design
// decision, see hjson2's busbar-topology history) — a Section is a
// per-connection slot, not a distinct point. Sections are assigned to
// whichever Equipment's own connects entry actually touches that shared
// node (see internal/exporter/hjson2/build.go's buildBusbarSections),
// which is not necessarily the "conceptual root" equipment of a branch
// (e.g. a Fuse directly touching the busbar, while the Transformer behind
// it keeps its own ordinary node). Section long IDs ("@BB-1-1") are local
// (see File's doc comment on the "@" convention) and are what a
// connecting Equipment's connects list references directly instead of a
// raw node ID.
type Busbar struct {
	ID       string               `json:"id"`
	Sections []BusbarSectionEntry `json:"sections"`
}

// Satellite is one folded "Anhängsel"/satellite object (e.g. a Wallbox
// folded into its owning PowerElectronicsConnection's Sachdaten, see
// internal/impl/common/sachdaten.go's satellite walk and
// AttributeKeySatellite's doc comment) — its own raw CIM class plus its
// own literal attributes, kept together as one self-contained object
// instead of being scattered across several parallel top-level Attributes
// arrays that would only stay correlated by coincidence.
type Satellite struct {
	Class      string                 `json:"class"`
	Attributes map[string]interface{} `json:"attributes"`
}

// BusbarSectionEntry is one section slot of a Busbar — its short ID
// ("1", "2", ...) is a sequence number, not an original CIM object ID (the
// original BusbarSection's own equipment ID is not recoverable from the
// persisted model, see this package's Busbar doc comment); Attributes/
// Satellites/Geometry are sourced best-effort from the station's original
// BusbarSection equipment objects, paired index-wise since an exact
// per-section mapping cannot be reconstructed. On import, every Section of
// one Busbar becomes its own synthetic BusbarSection Equipment sharing
// that Busbar's container — Phase 2's existing MergeBusbarSectionNodes
// then merges them all into one real electrical node, exactly like a
// multi-section busbar imported from real CIM/CGMES/NSC data (see
// resolve.go's emitStation).
type BusbarSectionEntry struct {
	ID         string                 `json:"id"`
	Attributes map[string]interface{} `json:"attributes"`
	Satellites []Satellite            `json:"satellites,omitempty"`
	// Geometry is this busbar section's own single WGS84 point (hjson2
	// only — see build.go's buildGeometryPath), reconstructed from a raw
	// CIM PowerSystemResource.Location -> PositionPoint chain instead of
	// being left as a raw "PositionPoint" satellite object. A section
	// only ever gets a single point, never a path (see this package's
	// GeometryPoint doc comment).
	Geometry *GeometryPoint `json:"geometry,omitempty"`
}

// Bay is one field (Abgangsfeld/Einspeisefeld) within a Substation/KVS
// file, holding its own Equipment list.
type Bay struct {
	ID        string      `json:"id"`
	Equipment []Equipment `json:"equipments"`
}

// Equipment is one piece of electrical equipment (Zweipol or, per the
// single-terminal source/sink convention, a one-entry connects list).
// Class must name a CIM class already known to the rest of the pipeline
// (Breaker, Disconnector, Fuse, GroundDisconnector, PowerTransformer,
// EnergyConsumer, PowerElectronicsConnection, ...).
type Equipment struct {
	ID         string                 `json:"id"`
	Class      string                 `json:"class"`
	Connects   []string               `json:"connects"`
	Attributes map[string]interface{} `json:"attributes"`
	Satellites []Satellite            `json:"satellites,omitempty"`
	// Geometry is this equipment's own single WGS84 point (hjson2 only —
	// see BusbarSectionEntry.Geometry's doc comment for the rationale;
	// same reconstruction, same single-point-only convention).
	Geometry *GeometryPoint `json:"geometry,omitempty"`
	// Measuring/Transmission are hjson2-only (Meter class only): a Meter
	// always folds exactly two TimeSchedule satellites (one via
	// Meter.MeasuringSchedule, one via Meter.TransmissionSchedule — see
	// build.go's buildEquipment), which otherwise show up as two
	// indistinguishable, identically-shaped generic "satellites" entries.
	// hjson2 special-cases Meter to instead surface them as these two
	// named blocks (each TimeSchedule's own attrs, "Class."-stripped) for
	// a much more compact/readable file. This is a positional heuristic
	// (first TimeSchedule satellite -> Measuring, second -> Transmission)
	// hardcoded in this package only — the real CIM relation name
	// (MeasuringSchedule vs. TransmissionSchedule) is not persisted by
	// the shared satellite-fold pipeline (internal/impl/common/
	// sachdaten.go), so it can't be recovered exactly; see this package's
	// history for why a shared-code change to preserve it was explicitly
	// declined in favor of this self-contained hjson2 approximation. Only
	// populated when a Meter has exactly two TimeSchedule satellites —
	// otherwise they fall back to the generic Satellites list unchanged.
	Measuring    map[string]interface{} `json:"measuring,omitempty"`
	Transmission map[string]interface{} `json:"transmission,omitempty"`
	// Steps is hjson2-only: a set of "DiscreteControlLimit" satellites
	// (as folded onto an equipment such as a PowerElectronicsConnection's
	// RegulatingControl, e.g. discrete charging/feed-in steps in %) is
	// otherwise a series of near-identical generic "satellites" entries
	// carrying only a sequenceNumber and a value. Reconstructed here as a
	// single compact bare-number array, ordered by ascending
	// sequenceNumber (Steps[i] corresponds to sequenceNumber i+1). Only
	// populated when every DiscreteControlLimit satellite present has a
	// parsable sequenceNumber/value and those sequence numbers are
	// exactly 1..N with no gaps/duplicates — otherwise they fall back to
	// the generic Satellites list unchanged. On import, each entry's name
	// is reconstructed as "<Equipment.ID>-<sequenceNumber>" (see
	// resolve.go's addDiscreteControlLimits) — a hjson2-only
	// approximation, not necessarily the original CIM name.
	Steps []float64 `json:"steps,omitempty"`
}

// Segment is one ACLineSegment within an ACLine ("Kabel") file. From/To
// name the two connection nodes — a cross-file reference (into a
// Substation/KVS/House file) must use the already-global ID (no leading
// "@", see File's doc comment on the "@" convention) verbatim; a same-file
// reference (rare, e.g. an inline splice between two of this file's own
// Segments) uses a local "@"-prefixed name like any other connects entry.
type Segment struct {
	ID         string                 `json:"id"`
	From       string                 `json:"from"`
	To         string                 `json:"to"`
	Attributes map[string]interface{} `json:"attributes"`
	Satellites []Satellite            `json:"satellites,omitempty"`
	// Geometry is this segment's full route (hjson2 only — see
	// BusbarSectionEntry.Geometry's doc comment for the general
	// rationale): unlike Equipment/BusbarSectionEntry, a cable/line
	// segment's PowerSystemResource.Location can carry several
	// PositionPoints (its route), so this is a sorted array (ascending
	// PositionPoint.sequenceNumber, first entry = sequenceNumber 1) rather
	// than a single point.
	Geometry []GeometryPoint `json:"geometry,omitempty"`
}
