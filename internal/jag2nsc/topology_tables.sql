-- topology_tables.sql - additive, opt-in storage for the precomputed, chain-collapsed
-- domain_model-shaped topology (terminal/connection/connection_terminal_map/line_segment).
--
-- These tables are ONLY created/populated when the NSC_SUPPORT feature is explicitly
-- requested (see cmd/jag2nsc-apply's -nsc-support flag / NSC_SUPPORT env var). They are
-- entirely additive next to the existing model_*/staging_* schema - no existing JAG table,
-- view, trigger, or Go source file is touched by this feature. When NSC_SUPPORT is not
-- used, these tables simply don't exist and views.sql's own (simpler, SQL-only, known-
-- granularity-limited) terminal/connection/connection_terminal_map/line_segment views keep
-- working exactly as before.
--
-- Rows here are keyed by JAG's natural external (text) ids, not by jag2nsc_id()'s hashed
-- bigints - the hashing happens only in the thin wrapper views (views_topology.sql) that
-- read from these tables, exactly like every other jag2nsc_* raw/staging object.
--
-- Population is full-replace (TRUNCATE + re-INSERT in one transaction) on every call to
-- topology.BuildTopology, consistent with the "full-replace-on-import" semantics documented
-- for the rest of this schema (cim-mapping.md).
BEGIN;

-- jag2nsc_raw_terminal mirrors the real CIM Terminal objects (id/ConductingEquipment/
-- ConnectivityNode/sequenceNumber) exactly as JAG's own staging_records still hold them
-- right after import, BEFORE any of JAG's own node-merging (MergeJunctionNodes/
-- MergeBusbarSectionNodes - see internal/impl/common) collapses a node-role Equipment's
-- several own ConnectivityNodes onto one canonical id. JAG's persisted model_edge/
-- model_node deliberately only records the POST-merge graph (that's the correct design for
-- JAG's own domain, see Konzept.md), so it has no way to tell jag2nsc that e.g. a
-- BusbarSection with 3 real Terminals to 3 different ConnectivityNodes actually had that
-- multiplicity - jag2nsc needs the RAW, per-Terminal multiplicity to reproduce the real
-- domain-model-connector's one-terminal-per-CIM-Terminal behavior faithfully.
--
-- This table is filled directly by jag2nsc's own Go code (topology.go's loadRawTerminals),
-- reading staging_records via plain SQL - no JAG Go source file is imported, called, or
-- modified to produce it. It only works while staging_records for the imported version
-- still exists (JAG's own import pipeline never deletes it automatically - see
-- internal/importer/phase1/run.go's doc comment - DeleteVersion is only ever called
-- explicitly by a caller that wants to reclaim the scratch space); if a deployment does
-- clear staging_records after import, this table (and therefore BuildTopology) simply has
-- nothing to read and produces an empty/incomplete topology - a known, accepted limitation
-- of this additive, external, read-only approach.
CREATE TABLE IF NOT EXISTS jag2nsc_raw_terminal
(
    terminal_id     text PRIMARY KEY, -- the real CIM Terminal object's own external id (e.g. "B-1-1-E-1")
    equipment_id    text NOT NULL,    -- Terminal.ConductingEquipment
    node_id         text NOT NULL,    -- Terminal.ConnectivityNode
    sequence_number integer           -- ACDCTerminal.sequenceNumber, NULL if absent in the source data
);
CREATE INDEX IF NOT EXISTS idx_jag2nsc_raw_terminal_equipment ON jag2nsc_raw_terminal (equipment_id);
CREATE INDEX IF NOT EXISTS idx_jag2nsc_raw_terminal_node ON jag2nsc_raw_terminal (node_id);

CREATE TABLE IF NOT EXISTS jag2nsc_topo_terminal
(
    terminal_key                text PRIMARY KEY,
    equipment_id                text NOT NULL, -- the CIM equipment this terminal instance belongs to (name/geometry/nominal_current source)
    cim_class                   text,
    network_device_external_id  text,          -- busbar/acline-container id, transformer equipment id, or house container id; NULL for a bare hub/junction terminal
    device_kind                 text,          -- 'TRANSFORMER' / 'BUSBAR' / 'HOUSE_CONNECTION' / NULL
    feeder_area_external_id     text,          -- bay container id, when this terminal sits on a feeder-end pattern; NULL otherwise
    terminal_type               text NOT NULL  -- 'FUSE' / 'SWITCH' / 'NOT_SWITCHABLE'
);

CREATE TABLE IF NOT EXISTS jag2nsc_topo_connection
(
    connection_key      text PRIMARY KEY,
    external_id          text NOT NULL,
    source_terminal_key text NOT NULL REFERENCES jag2nsc_topo_terminal (terminal_key),
    target_terminal_key text NOT NULL REFERENCES jag2nsc_topo_terminal (terminal_key),
    source_device_kind  text,
    target_device_kind  text
);
CREATE INDEX IF NOT EXISTS idx_jag2nsc_connection_source ON jag2nsc_topo_connection (source_terminal_key);
CREATE INDEX IF NOT EXISTS idx_jag2nsc_connection_target ON jag2nsc_topo_connection (target_terminal_key);

CREATE TABLE IF NOT EXISTS jag2nsc_topo_connection_terminal_map
(
    connection_key  text NOT NULL REFERENCES jag2nsc_topo_connection (connection_key),
    terminal_key    text NOT NULL REFERENCES jag2nsc_topo_terminal (terminal_key),
    sequence_number integer NOT NULL,
    PRIMARY KEY (connection_key, sequence_number)
);
CREATE INDEX IF NOT EXISTS idx_jag2nsc_ctm_terminal ON jag2nsc_topo_connection_terminal_map (terminal_key);

CREATE TABLE IF NOT EXISTS jag2nsc_topo_line_segment
(
    line_key         text PRIMARY KEY,
    connection_key   text NOT NULL REFERENCES jag2nsc_topo_connection (connection_key),
    acline_equipment_id text NOT NULL,
    sequence_number  integer NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_jag2nsc_line_segment_connection ON jag2nsc_topo_line_segment (connection_key);

COMMIT;
