// Package hjson implements the export half of the Fachmodell HJSON dialect
// (see internal/importer/hjson's doc comment and Konzept.md's "HJSON
// Fachmodell" section, "Export"): read a whole persisted model back out of
// ModelStore and write it as a directory tree of hand-readable *.hjson
// files, symmetric to the import direction.
//
// Known current limitations (documented, not yet implemented — matches the
// importer's own documented gaps):
//   - Container-level custom Sachdaten (e.g. a Substation's
//     "station_kind"/"region", a House's MaLo/MeLo) are not exported,
//     since Phase 2 doesn't persist them in the first place (see
//     internal/importer/hjson's Emit doc comment).
//   - ID-shortening only strips a "<containerID>-" prefix if present (the
//     Fachmodell importer's own prefixing scheme, see Konzept.md); raw
//     CIM/CGMES/NSC-imported data (whose IDs never had that prefix
//     applied) is exported with its full original IDs unchanged — this is
//     the documented "fails gracefully to full ID for raw-CIM-origin
//     elements" behavior, not a bug.
//   - AllAttributes pages by owner_id with a fixed limit (see
//     internal/sqlite/model_export.go); an owner with more attribute rows
//     than the page limit could in theory be split across two pages. A
//     generously large limit is used to make this practically irrelevant
//     for the dataset sizes this exporter currently targets.
package hjson2
