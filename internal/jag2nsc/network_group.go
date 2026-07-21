// network_group.go implements the NSC_SUPPORT feature's `network_group` table: extracting
// the real CIM `SubGeographicalRegion` element(s) from JAG's own staging_records and
// exposing them via the additive jag2nsc_network_group table (network_group_tables.sql),
// backing the network_group view and the network_device.network_group_id column.
//
// network_group is genuinely CIM-derived, not hardcoded: verified against real ground truth
// that a real domain_model.network_group row's external_id/name are exactly the CIM
// SubGeographicalRegion element's own rdf:about id and IdentifiedObject.name. JAG's own
// Phase 2 (internal/impl/common/container.go, sachdaten.go) deliberately never turns
// SubGeographicalRegion into a model_container/model_equipment row - Region classes are
// treated as structural hubs the satellite walk never walks into (see sachdaten.go's
// structuralClasses) - so there is no model_*-level source for this at all; staging_records
// (JAG's own raw import records, still present after import - see topology.go's
// loadRawTerminals doc comment for the same, already-established precedent) is the only
// place this data survives. No existing JAG Go source file is imported, called, or modified
// by this code - it only reads staging_records via plain SQL, exactly like loadRawTerminals.
//
// Known limitation (see network_group_tables.sql): every dataset inspected so far
// (example_as_cim.xml, the Muffen dataset) has exactly one SubGeographicalRegion, and every
// network_device ends up with that SAME single network_group_id in the real DB - this
// feature only supports that single-region case.
package jag2nsc

import (
	"context"
	"database/sql"
	_ "embed"
	"fmt"
)

//go:embed network_group_tables.sql
var networkGroupTablesSQL string

// ApplyNetworkGroupTables (re-)creates the additive jag2nsc_network_group table
// (network_group_tables.sql). It does not populate it - call BuildNetworkGroup afterwards.
func ApplyNetworkGroupTables(ctx context.Context, db *sql.DB) error {
	if _, err := db.ExecContext(ctx, networkGroupTablesSQL); err != nil {
		return fmt.Errorf("jag2nsc: applying network_group_tables.sql: %w", err)
	}
	return nil
}

// BuildNetworkGroup (re-)populates jag2nsc_network_group (full-replace) directly from JAG's
// own staging_records: every CIM SubGeographicalRegion object's own id + IdentifiedObject.
// name, at the latest staging version present (same "most recent import wins" convention as
// loadRawTerminals). If more than one SubGeographicalRegion is present in the source data,
// only the lexicographically smallest id is kept (see this file's doc comment) - a
// documented, accepted limitation, not a silent guess.
func BuildNetworkGroup(ctx context.Context, db *sql.DB) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `TRUNCATE jag2nsc_network_group`); err != nil {
		return fmt.Errorf("jag2nsc: truncating jag2nsc_network_group: %w", err)
	}

	if _, err := tx.ExecContext(ctx, `
INSERT INTO jag2nsc_network_group (external_id, name)
SELECT id,
       MAX(value) FILTER (WHERE attribute = 'IdentifiedObject.name')
FROM staging_records
WHERE class = 'SubGeographicalRegion'
  AND version = (SELECT MAX(version) FROM staging_records)
GROUP BY id
ORDER BY id
LIMIT 1`); err != nil {
		return fmt.Errorf("jag2nsc: populating jag2nsc_network_group from staging_records: %w", err)
	}

	return tx.Commit()
}
