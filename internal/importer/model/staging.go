package model

// StagingRecord is one dialect-neutral raw import fact, produced by Phase 1
// parsing (see Konzept.md, "Die Import-Pipeline"). Version identifies the
// import run this record belongs to: every import run is assigned a fresh,
// monotonically increasing version (see staging.Store.NextVersion) so
// repeated imports never collide in the staging tables. Once the import
// has fully completed (its data has been consumed into the node-edge
// model), the run deletes its own version's rows from staging again — the
// staging tables are a transient scratch area, not permanent storage.
type StagingRecord struct {
	Version     uint64
	ID          string
	Profile     string
	Class       string
	Attribute   string
	Value       string
	IsReference bool
	Seq         int
}

// StagingError is one Phase 1 parse error, collected instead of aborting the
// whole run (see Idee.md "Implementierungshinweise": a phase runs to
// completion, all errors are gathered and reported, not just the first one).
type StagingError struct {
	Version    uint64
	SourceFile string
	Line       int64
	ByteOffset int64
	Message    string
}
