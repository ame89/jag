// Package jag2nsc applies the read-only, domain_model-shaped SQL VIEW layer (views.sql)
// and the locked-down reader role (reader_role.sql) to a Postgres database that already
// contains an imported JAG "model_*"/"staging_*" schema (see example_as_cim/MAPPING.md).
//
// Both SQL files are embedded at compile time so the resulting binary is self-contained -
// no separate file deployment/versioning step is needed to keep the SQL in sync with the
// Go code that applies it.
package jag2nsc

import (
	"context"
	"database/sql"
	_ "embed"
	"fmt"
)

//go:embed views.sql
var viewsSQL string

//go:embed reader_role.sql
var readerRoleSQL string

//go:embed views_topology.sql
var viewsTopologySQL string

//go:embed views_circuit.sql
var viewsCircuitSQL string

//go:embed views_placeholders.sql
var viewsPlaceholdersSQL string

// ApplyViews (re-)creates every jag2nsc_* helper object and all ten domain_model-shaped
// views (container, network_device, transformer, busbar, house_connection, feeder_area,
// terminal, connection, connection_terminal_map, line_segment) against db.
//
// It is idempotent: it can be called again after every JAG re-import without manual
// cleanup, and it also (re-)installs the jag2nsc_terminal_idx sync table + its triggers on
// model_edge/model_equipment/model_attribute so that future changes to the source data are
// reflected in the views near-instantly.
//
// db must be connected to the SAME database the JAG model was imported into.
func ApplyViews(ctx context.Context, db *sql.DB) error {
	if _, err := db.ExecContext(ctx, viewsSQL); err != nil {
		return fmt.Errorf("jag2nsc: applying views.sql: %w", err)
	}
	return nil
}

// ApplyTopologyViews overrides views.sql's own terminal/connection/connection_terminal_map/
// line_segment view definitions with thin wrappers over the NSC_SUPPORT feature's
// Go-computed jag2nsc_terminal/jag2nsc_connection/jag2nsc_connection_terminal_map/
// jag2nsc_line_segment tables (see views_topology.sql / topology.go). ApplyViews,
// ApplyTopologyTables, and BuildTopology must all have been run first (in that order) -
// this only swaps the view definitions, it does not create the tables or compute their
// contents.
func ApplyTopologyViews(ctx context.Context, db *sql.DB) error {
	if _, err := db.ExecContext(ctx, viewsTopologySQL); err != nil {
		return fmt.Errorf("jag2nsc: applying views_topology.sql: %w", err)
	}
	return nil
}

// ApplyCircuitViews overrides views.sql's own network_device view (wiring net_island/
// network_group_id) and adds the circuit/circuit_network_device_map/network_group views,
// backed by the NSC_SUPPORT feature's Go-computed jag2nsc_circuit/jag2nsc_circuit_member/
// jag2nsc_network_group tables (see views_circuit.sql / circuits.go / network_group.go).
// ApplyViews, ApplyCircuitTables, ApplyNetworkGroupTables, BuildCircuits, and
// BuildNetworkGroup must all have been run first - this only swaps/adds view definitions,
// it does not create the tables or compute their contents.
func ApplyCircuitViews(ctx context.Context, db *sql.DB) error {
	if _, err := db.ExecContext(ctx, viewsCircuitSQL); err != nil {
		return fmt.Errorf("jag2nsc: applying views_circuit.sql: %w", err)
	}
	return nil
}

// ApplyPlaceholderViews adds the eight domain_model-shaped views jag2nsc has no full CIM/JAG
// raw data source for yet (threshold/threshold_history, switching_state/
// switching_state_history, circuit_config/circuit_config_history, sensitivity/
// sensitivity_history). threshold/threshold_history reproduce the static SYSTEM-default
// rows domain_model itself seeds via Flyway migration (not derived from any CIM content);
// the other six stay permanently empty. See views_placeholders.sql for the full rationale
// and per-table status. Always applied by ApplyViews - not gated behind NSC_SUPPORT, since
// none of this needs the Go-computed topology/circuit feature.
func ApplyPlaceholderViews(ctx context.Context, db *sql.DB) error {
	if _, err := db.ExecContext(ctx, viewsPlaceholdersSQL); err != nil {
		return fmt.Errorf("jag2nsc: applying views_placeholders.sql: %w", err)
	}
	return nil
}

// ApplyReaderRole (re-)creates the jag2nsc_reader role and (re-)grants it SELECT on
// exactly the ten public domain_model-shaped views - never on the underlying model_*/
// staging_* tables, and never any write privilege on anything (see reader_role.sql for the
// full defense-in-depth rationale). ApplyViews must have been run first, since the GRANT
// statements reference the views by name.
//
// If password is non-empty, the role's login password is set/rotated in the same call via
// a parameterized ALTER ROLE (never interpolated into the embedded SQL text, so it never
// ends up logged or embedded in the binary).
func ApplyReaderRole(ctx context.Context, db *sql.DB, password string) error {
	if _, err := db.ExecContext(ctx, readerRoleSQL); err != nil {
		return fmt.Errorf("jag2nsc: applying reader_role.sql: %w", err)
	}
	if password != "" {
		// Role names cannot be parameterized as bind values in Postgres; jag2nsc_reader is
		// a fixed, hardcoded identifier (not user input), so building the statement is safe.
		if _, err := db.ExecContext(ctx, `SELECT set_config('jag2nsc.reader_password', $1, false)`, password); err != nil {
			return fmt.Errorf("jag2nsc: staging reader password: %w", err)
		}
		if _, err := db.ExecContext(ctx, `DO $$
DECLARE
    pw text := current_setting('jag2nsc.reader_password');
BEGIN
    EXECUTE format('ALTER ROLE jag2nsc_reader WITH PASSWORD %L', pw);
END
$$;`); err != nil {
			return fmt.Errorf("jag2nsc: setting reader password: %w", err)
		}
	}
	return nil
}
