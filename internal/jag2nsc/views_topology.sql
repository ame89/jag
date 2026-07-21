-- views_topology.sql - the NSC_SUPPORT variant of the four topology views
-- (terminal/connection/connection_terminal_map/line_segment).
--
-- Applied ONLY when the NSC_SUPPORT feature is requested (see cmd/jag2nsc-apply's
-- -nsc-support flag / NSC_SUPPORT env var), AFTER views.sql and topology_tables.sql have
-- already been applied and BuildTopology has populated jag2nsc_topo_terminal/jag2nsc_topo_connection/
-- jag2nsc_topo_connection_terminal_map/jag2nsc_topo_line_segment.
--
-- These four CREATE OR REPLACE VIEW statements simply override views.sql's own (simpler,
-- purely-SQL, node-degree-based) definitions of the same four view names with thin wrappers
-- around the Go-computed, chain-collapsed topology tables - same column names/order/types,
-- so PostgreSQL accepts the replacement in place without dropping/recreating dependents
-- (e.g. terminal_feeder_end_extension, which reads from `terminal` and needs no changes at
-- all either way).
--
-- Not exact 1:1 parity with the real domain-model-connector's own internal terminal
-- bookkeeping (see topology.go's package doc comment for the documented, deliberate
-- simplifications) - the goal is a topologically-correct graph with realistic element
-- counts, not byte-for-byte identical terminal semantics.
BEGIN;

CREATE OR REPLACE VIEW terminal AS
SELECT jag2nsc_id(t.terminal_key)                                          AS id,
       t.terminal_key                                                      AS external_id,
       n.name,
       CASE WHEN t.network_device_external_id IS NOT NULL
                THEN jag2nsc_id(t.network_device_external_id) END          AS network_device_id,
       CASE WHEN t.feeder_area_external_id IS NOT NULL
                THEN jag2nsc_id(t.feeder_area_external_id) END             AS feeder_area_id,
       t.terminal_type::terminal_type                                      AS type,
       TRUE                                                                AS default_switching_state,
       fuse.nominal_current                                                AS fuse_value,
       fuse.nominal_current                                                AS fuse_nominal_current,
       eg.lat                                                              AS latitude,
       eg.lon                                                              AS longitude
FROM jag2nsc_topo_terminal t
         LEFT JOIN jag2nsc_display_name n ON n.owner_id = t.equipment_id
         LEFT JOIN jag2nsc_equipment_geometry eg ON eg.equipment_id = t.equipment_id
         LEFT JOIN (
    SELECT owner_id AS equipment_id, jag2nsc_attr_text(value)::integer AS nominal_current
    FROM model_attribute
    WHERE key = 'Fuse.nominalCurrent'
      AND seq = 0
    ) fuse ON fuse.equipment_id = t.equipment_id;

CREATE OR REPLACE VIEW connection AS
SELECT jag2nsc_id(c.connection_key)          AS id,
       c.external_id,
       jag2nsc_id(c.source_terminal_key)     AS source_id,
       c.source_device_kind::device_type     AS source_device_type,
       jag2nsc_id(c.target_terminal_key)     AS target_id,
       c.target_device_kind::device_type     AS target_device_type,
       TRUE                                   AS is_active
FROM jag2nsc_topo_connection c;

CREATE OR REPLACE VIEW connection_terminal_map AS
SELECT jag2nsc_id(m.connection_key || '~' || m.terminal_key) AS id,
       jag2nsc_id(m.connection_key)                           AS connection_id,
       jag2nsc_id(m.terminal_key)                              AS terminal_id,
       m.sequence_number
FROM jag2nsc_topo_connection_terminal_map m;

CREATE OR REPLACE VIEW line_segment AS
SELECT jag2nsc_id(l.line_key)             AS id,
       jag2nsc_id(l.connection_key)        AS connection_id,
       l.acline_equipment_id               AS external_id,
       l.sequence_number,
       len.value                           AS length,
       r.value                             AS r,
       NULL::double precision              AS x,
       NULL::double precision              AS bch,
       NULL::double precision              AS total_resistance,
       pos.path_positions                  AS path_positions
FROM jag2nsc_topo_line_segment l
         LEFT JOIN (SELECT owner_id, jag2nsc_attr_text(value)::double precision AS value
                    FROM model_attribute WHERE key = 'Conductor.length' AND seq = 0) len ON len.owner_id = l.acline_equipment_id
         LEFT JOIN (SELECT owner_id, jag2nsc_attr_text(value)::double precision AS value
                    FROM model_attribute WHERE key = 'ACLineSegment.r' AND seq = 0) r ON r.owner_id = l.acline_equipment_id
         LEFT JOIN LATERAL (
    SELECT jsonb_agg(jsonb_build_object(
                   'sequenceNumber', (s.attrs ->> 'PositionPoint.sequenceNumber')::integer,
                   'x', (s.attrs ->> 'PositionPoint.xPosition')::double precision,
                   'y', (s.attrs ->> 'PositionPoint.yPosition')::double precision
           ) ORDER BY (s.attrs ->> 'PositionPoint.sequenceNumber')::integer) AS path_positions
    FROM jag2nsc_satellite s
    WHERE s.owner_id = l.acline_equipment_id AND s.class = 'PositionPoint'
    ) pos ON TRUE;

COMMIT;
