-- network_group_tables.sql - additive, opt-in storage for the Go-extracted `network_group`
-- feature (part of NSC_SUPPORT, see network_group.go).
--
-- Entirely additive next to model_*/staging_*: no existing JAG table, view, trigger, or Go
-- source file is touched. Population is full-replace (TRUNCATE + re-INSERT in one
-- transaction) on every call to BuildNetworkGroup, consistent with the rest of this
-- schema's full-replace-on-import semantics.
BEGIN;

-- jag2nsc_network_group mirrors the real domain-model-connector's NetworkGroup: it is
-- genuinely CIM-derived, not hardcoded - it comes directly from CIM's SubGeographicalRegion
-- element (external_id = its own rdf:about id, name = its IdentifiedObject.name). JAG's own
-- Phase 2 (container.go/sachdaten.go) never turns SubGeographicalRegion into a
-- model_container/model_equipment row (Region classes are treated as structural hubs, never
-- walked into) - so this table is filled directly from staging_records, the same
-- established pattern topology.go's loadRawTerminals already uses.
--
-- Known limitation: every real dataset inspected so far (example_as_cim.xml, the Muffen
-- dataset) has exactly one SubGeographicalRegion, and every network_device in the real DB
-- ends up with the SAME single network_group_id - so this table/feature only supports the
-- single-SubGeographicalRegion case: if a CIM source ever had more than one, only the
-- first (lexicographically smallest id) is used and assigned to every network_device.
CREATE TABLE IF NOT EXISTS jag2nsc_network_group
(
    external_id text PRIMARY KEY,
    name        text
);

COMMIT;
