-- views_circuit.sql - the NSC_SUPPORT variant of `network_device` (wiring net_island/
-- network_group_id) plus the new `circuit`, `circuit_network_device_map`, and
-- `network_group` views.
--
-- Applied ONLY when the NSC_SUPPORT feature is requested (see cmd/jag2nsc-apply's
-- -nsc-support flag / NSC_SUPPORT env var), AFTER views.sql, circuit_tables.sql, and
-- network_group_tables.sql have already been applied and BuildCircuits/BuildNetworkGroup
-- have populated jag2nsc_circuit/jag2nsc_circuit_member/jag2nsc_network_group.
--
-- See circuits.go/network_group.go for the full derivation rationale (circuit membership
-- reuses JAG's own common.BuildCircuits; naming approximates the real connector's
-- PowerTransformerEnd-based convention; network_group is extracted from CIM
-- SubGeographicalRegion via staging_records - not hardcoded).
BEGIN;

CREATE VIEW circuit AS
SELECT jag2nsc_id(c.external_id) AS id,
       c.external_id,
       c.name
FROM jag2nsc_circuit c;

CREATE VIEW circuit_network_device_map AS
SELECT jag2nsc_id(c.external_id || '~' || m.device_id) AS id,
       jag2nsc_id(c.external_id)                        AS circuit_id,
       jag2nsc_id(m.device_id)                           AS network_device_id
FROM jag2nsc_circuit_member m
         JOIN jag2nsc_circuit c ON c.circuit_key = m.circuit_key;

CREATE VIEW network_group AS
SELECT jag2nsc_id(g.external_id) AS id,
       g.external_id,
       g.name
FROM jag2nsc_network_group g;

-- Overrides views.sql's own network_device (which leaves net_island/network_group_id NULL
-- for every row - see its comment "persistor/connector-only concepts with no JAG source
-- data at all"): net_island now comes from circuit_network_device_map, and
-- network_group_id from the single jag2nsc_network_group row (every dataset inspected so
-- far has exactly one SubGeographicalRegion, so every network_device gets the same group -
-- matches the real DB exactly, see network_group_tables.sql's doc comment).
CREATE OR REPLACE VIEW network_device AS
SELECT nd.id,
       NULL::character varying AS external_id,
       NULL::character varying AS name,
       cnd.circuit_id           AS net_island,
       ng.id                    AS network_group_id
FROM (
         SELECT id FROM transformer
         UNION ALL
         SELECT id FROM busbar
         UNION ALL
         SELECT id FROM house_connection
     ) nd
         LEFT JOIN circuit_network_device_map cnd ON cnd.network_device_id = nd.id
         LEFT JOIN LATERAL (SELECT id FROM network_group ORDER BY id LIMIT 1) ng ON TRUE;

COMMIT;
