-- circuit_tables.sql - additive, opt-in storage for the Go-computed `circuit` /
-- `circuit_network_device_map` feature (part of NSC_SUPPORT, see circuits.go).
--
-- Entirely additive next to model_*/staging_*: no existing JAG table, view, trigger, or Go
-- source file is touched. Population is full-replace (TRUNCATE + re-INSERT in one
-- transaction) on every call to BuildCircuits, consistent with the rest of this schema's
-- full-replace-on-import semantics.
BEGIN;

-- jag2nsc_circuit: one row per computed Circuit ("Schaltkreis") - a connected component of
-- the physical Node/Edge graph, using the exact same PowerTransformer-boundary / open-
-- switch-interrupts / GND-excluded rules as the real domain-model-connector's own
-- NetIsland computation (see circuits.go's doc comment, and
-- internal/impl/common/electrical.go's BuildCircuits, reused as-is via its exported API -
-- no jag core file is modified).
--
-- circuit_key is jag's own internal Union-Find group id (Circuit.ID - lexicographically
-- smallest member Node ID), used only to join circuit_key -> jag2nsc_circuit_member. It is
-- NOT the exposed external_id (see below).
CREATE TABLE IF NOT EXISTS jag2nsc_circuit
(
    circuit_key text PRIMARY KEY,
    external_id text NOT NULL, -- "<PowerTransformerEnd mRID>-Island", mirroring the real connector's CimToCondensedService.createNetIslandMapping
    name        text NOT NULL  -- that PowerTransformerEnd's own IdentifiedObject.name, falling back to its mRID if unnamed
);

-- jag2nsc_circuit_member: which network_device (transformer/busbar/house_connection)
-- equipment/container ids belong to which Circuit - backs the circuit_network_device_map
-- view and the network_device.net_island column.
CREATE TABLE IF NOT EXISTS jag2nsc_circuit_member
(
    circuit_key text NOT NULL REFERENCES jag2nsc_circuit (circuit_key),
    device_id   text NOT NULL, -- equipment id (transformer) or container id (busbar/house_connection) - same convention jag2nsc_id() is applied to everywhere else
    PRIMARY KEY (circuit_key, device_id)
);
CREATE INDEX IF NOT EXISTS idx_jag2nsc_circuit_member_device ON jag2nsc_circuit_member (device_id);

COMMIT;
