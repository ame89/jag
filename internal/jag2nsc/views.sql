-- jag2nsc: read-only SQL VIEW layer that projects a JAG "model_*" schema
-- (internal/sqlite + internal/postgres model store, see example_as_cim/MAPPING.md)
-- onto a schema shaped like org-openk-nsc-domain-model-config-persistor's `domain_model`
-- database (see schema.md / cim-mapping.md in the repo root).
--
-- Design goals (see README.md for the full rationale):
--   * Read-only: every mapped table is a VIEW, never a materialized copy. The source
--     model_* tables are never modified.
--   * Generic: the views operate purely on the model_* table shapes (model_equipment,
--     model_node, model_edge, model_edge_endpoint, model_container, model_attribute,
--     model_geometry, model_electrical_group) - they are NOT hand-tuned for one dataset.
--   * Best-effort for fields the CIM/JAG source genuinely has no data for (documented
--     inline below and in README.md "Known limitations").
--
-- This script is idempotent: it can be re-run after every re-import (DROP ... IF EXISTS
-- CASCADE followed by CREATE). It must be applied to the SAME database the JAG model was
-- imported into (schemas: model_*, staging_* stay untouched).

BEGIN;

-- ---------------------------------------------------------------------------------------
-- 0. Enum types mirroring domain_model (safe to (re)create; DROP ... CASCADE below first)
-- ---------------------------------------------------------------------------------------
DROP VIEW IF EXISTS terminal_feeder_end_extension CASCADE;
DROP VIEW IF EXISTS steua_step CASCADE;
DROP VIEW IF EXISTS steua CASCADE;
DROP VIEW IF EXISTS measuring_device CASCADE;
DROP VIEW IF EXISTS connection_terminal_map CASCADE;
DROP VIEW IF EXISTS line_segment CASCADE;
DROP VIEW IF EXISTS connection CASCADE;
DROP VIEW IF EXISTS terminal CASCADE;
DROP VIEW IF EXISTS feeder_area CASCADE;
DROP VIEW IF EXISTS house_connection CASCADE;
DROP VIEW IF EXISTS busbar CASCADE;
DROP VIEW IF EXISTS transformer CASCADE;
DROP VIEW IF EXISTS network_device CASCADE;
DROP VIEW IF EXISTS container CASCADE;

DROP VIEW IF EXISTS jag2nsc_steua_unit CASCADE;
DROP VIEW IF EXISTS jag2nsc_satellite CASCADE;
DROP VIEW IF EXISTS jag2nsc_node_members CASCADE;
DROP VIEW IF EXISTS jag2nsc_terminal CASCADE;
DROP VIEW IF EXISTS jag2nsc_terminal_raw CASCADE;
DROP VIEW IF EXISTS jag2nsc_transformer_satellite CASCADE;
DROP VIEW IF EXISTS jag2nsc_equipment_geometry CASCADE;
DROP VIEW IF EXISTS jag2nsc_equipment_class CASCADE;
DROP VIEW IF EXISTS jag2nsc_display_name CASCADE;

DROP TRIGGER IF EXISTS trg_jag2nsc_sync_model_edge ON model_edge;
DROP TRIGGER IF EXISTS trg_jag2nsc_sync_model_edge_delete ON model_edge;
DROP TRIGGER IF EXISTS trg_jag2nsc_sync_model_equipment ON model_equipment;
DROP TRIGGER IF EXISTS trg_jag2nsc_sync_model_attribute_cim_class ON model_attribute;
DROP TRIGGER IF EXISTS trg_jag2nsc_sync_model_attribute_cim_class_delete ON model_attribute;
DROP TRIGGER IF EXISTS trg_jag2nsc_sync_model_attribute_busbar_node ON model_attribute;
DROP TRIGGER IF EXISTS trg_jag2nsc_sync_model_attribute_busbar_node_delete ON model_attribute;
DROP FUNCTION IF EXISTS jag2nsc_terminal_idx_refresh(text) CASCADE;
DROP FUNCTION IF EXISTS jag2nsc_terminal_idx_refresh_busbar(text) CASCADE;
DROP TABLE IF EXISTS jag2nsc_terminal_idx CASCADE;

DROP FUNCTION IF EXISTS jag2nsc_id(text);
DROP FUNCTION IF EXISTS jag2nsc_attr_text(text) CASCADE;

DROP TYPE IF EXISTS container_type CASCADE;
DROP TYPE IF EXISTS device_type CASCADE;
DROP TYPE IF EXISTS steua_type CASCADE;
DROP TYPE IF EXISTS steua_role CASCADE;
DROP TYPE IF EXISTS steua_control_type CASCADE;
DROP TYPE IF EXISTS sign_convention CASCADE;
DROP TYPE IF EXISTS terminal_type CASCADE;

CREATE TYPE container_type AS ENUM ('SUBSTATION', 'DISTRIBUTION_BOX');
CREATE TYPE device_type AS ENUM ('TRANSFORMER', 'BUSBAR', 'HOUSE_CONNECTION');
CREATE TYPE terminal_type AS ENUM ('SWITCH', 'FUSE', 'NOT_SWITCHABLE');
CREATE TYPE steua_type AS ENUM ('STORAGE', 'WALLBOX', 'AIRCONDITION', 'HEATPUMP', 'ENERGYMANAGEMENTSYSTEM');
CREATE TYPE steua_role AS ENUM ('PRODUCER', 'CONSUMER');
CREATE TYPE steua_control_type AS ENUM ('STEPLESS', 'STEPPED');
CREATE TYPE sign_convention AS ENUM ('LOAD', 'GENERATOR');

-- ---------------------------------------------------------------------------------------
-- 1. Helpers
-- ---------------------------------------------------------------------------------------

-- domain_model uses BIGINT surrogate keys derived deterministically from `external_id`
-- (MD5 hash of the external id, see cim-mapping.md §4 "NetworkElementIdGenerator"). We
-- reproduce a *stable* (but NOT bit-identical to the Java implementation) BIGINT here so
-- that `id` columns are always non-null and deterministic across re-runs. Consumers that
-- need to correlate rows across imports should prefer `external_id`, exactly as
-- cim-mapping.md §4 recommends.
CREATE FUNCTION jag2nsc_id(external_id text) RETURNS bigint
    LANGUAGE sql IMMUTABLE PARALLEL SAFE AS
$$
SELECT ('x' || substr(md5(external_id), 1, 16))::bit(64)::bigint;
$$;

-- model_attribute.value is always a JSON-encoded scalar (even plain strings/numbers/bools
-- are quoted, e.g. `"Fuse"`, `"true"`, `"16"` - see example_as_cim/MAPPING.md §5). This
-- strips the JSON quoting down to a plain text value.
CREATE FUNCTION jag2nsc_attr_text(raw text) RETURNS text
    LANGUAGE sql IMMUTABLE PARALLEL SAFE AS
$$
SELECT CASE WHEN raw IS NULL THEN NULL ELSE trim(both '"' from raw) END;
$$;

-- Best-effort display name per owner_id (equipment OR container): JAG stores both a
-- synthetic `name` key and the raw CIM `IdentifiedObject.name` - prefer the synthetic one.
CREATE VIEW jag2nsc_display_name AS
SELECT owner_id,
       COALESCE(
           max(jag2nsc_attr_text(value)) FILTER (WHERE key = 'name'),
           max(jag2nsc_attr_text(value)) FILTER (WHERE key = 'IdentifiedObject.name')
       ) AS name
FROM model_attribute
WHERE key IN ('name', 'IdentifiedObject.name')
  AND seq = 0
GROUP BY owner_id;

-- The raw CIM class per equipment id (`Fuse`, `ACLineSegment`, `PowerTransformer`, ...).
CREATE VIEW jag2nsc_equipment_class AS
SELECT owner_id AS equipment_id, jag2nsc_attr_text(value) AS cim_class
FROM model_attribute
WHERE key = 'cim_class'
  AND seq = 0;

CREATE VIEW jag2nsc_equipment_geometry AS
SELECT owner_id AS equipment_id, lat, lon
FROM model_geometry
WHERE owner_kind = 'equipment';

-- Generic parser for `model_attribute.key = 'satellite'` rows: each one is a nested CIM
-- object (`PowerTransformerEnd`, `RegulatingControl`, `DiscreteControlLimit`, `UsagePoint`,
-- `UsagePointLocation`, `PositionPoint`, the concrete PowerElectronicsUnit subtype, ...)
-- attached to an owning equipment, JSON-encoded as `{"class": "...", "attributes": {...}}`
-- (see example_as_cim/MAPPING.md §5). This unlocks several tables previously assumed to
-- have no JAG source data at all - see README.md "Known limitations" for the full list of
-- what each satellite class now feeds.
CREATE VIEW jag2nsc_satellite AS
SELECT owner_id,
       seq,
       jag2nsc_attr_text(value)::jsonb ->> 'class'      AS class,
       jag2nsc_attr_text(value)::jsonb -> 'attributes'   AS attrs
FROM model_attribute
WHERE key = 'satellite';

-- The concrete PowerElectronicsUnit subtype satellite (`AirConditioningUnit`, `Heatpump`,
-- `BatteryUnit`, `PhotoVoltaicUnit`) attached to a PowerElectronicsConnection equipment -
-- this is the steua-relevant "unit type" signal (see §8 below). Assumes at most one such
-- satellite per owning equipment (matches every dataset inspected so far).
CREATE VIEW jag2nsc_steua_unit AS
SELECT owner_id AS equipment_id, class AS unit_class, attrs
FROM jag2nsc_satellite
WHERE class IN ('AirConditioningUnit', 'Heatpump', 'BatteryUnit', 'PhotoVoltaicUnit');

-- Folded satellite attributes for PowerTransformerEnd (see MAPPING.md §5 "satellite").
-- A PowerTransformer typically has two PowerTransformerEnd satellites (HV/LV side); we
-- take MAX() as a best-effort single value per transformer (real per-side data would need
-- a `transformer_end` table, which domain_model does not model separately either).
CREATE VIEW jag2nsc_transformer_satellite AS
SELECT owner_id                                                                     AS equipment_id,
       max((jag2nsc_attr_text(value)::jsonb #>> '{attributes,PowerTransformerEnd.ratedS}')::double precision)                AS nominal_apparent_power,
       max((jag2nsc_attr_text(value)::jsonb #>> '{attributes,PowerTransformerEnd.maxApparentPowerFactor}')::double precision) AS max_apparent_power_factor
FROM model_attribute
WHERE key = 'satellite'
  AND jag2nsc_attr_text(value)::jsonb ->> 'class' = 'PowerTransformerEnd'
GROUP BY owner_id;

-- ---------------------------------------------------------------------------------------
-- 2. container  <=  model_container rows of type 'substation' / 'distribution-box'
--
-- JAG's other container types (`bay`, `busbar`, `acline`, `house`) do NOT correspond to a
-- domain_model `container` row - they map onto feeder_area / busbar(-device) / connection
-- chains / house_connection instead (see below).
-- ---------------------------------------------------------------------------------------
CREATE VIEW container AS
SELECT jag2nsc_id(c.id)                                   AS id,
       c.id                                                AS external_id,
       n.name,
       CASE c.type
           WHEN 'substation' THEN 'SUBSTATION'
           WHEN 'distribution-box' THEN 'DISTRIBUTION_BOX'
           END::container_type                             AS container_type,
       NULL::varchar(255)                                  AS address, -- no postal address in JAG source data
       g.lat                                                AS latitude,
       g.lon                                                AS longitude
FROM model_container c
         LEFT JOIN jag2nsc_display_name n ON n.owner_id = c.id
         LEFT JOIN model_geometry g ON g.owner_id = c.id AND g.owner_kind = 'container'
WHERE c.type IN ('substation', 'distribution-box');

-- ---------------------------------------------------------------------------------------
-- 3. network_device (+ subtypes transformer / busbar / house_connection)
--
--   transformer        <= equipment with cim_class = 'PowerTransformer'
--                          (a PowerTransformer IS a two-terminal edge in JAG, exactly
--                          like domain_model's Zweipol assumption, see MAPPING.md §4)
--   busbar              <= model_container rows of type 'busbar' (JAG models an entire
--                          named busbar as one container + one implicit Node, not per
--                          BusbarSection equipment, see MAPPING.md §3)
--   house_connection    <= model_container rows of type 'house'
-- ---------------------------------------------------------------------------------------
CREATE VIEW transformer AS
SELECT jag2nsc_id(e.id)                        AS id,
       jag2nsc_id(e.container_id)               AS container_id,
       1                                        AS strip_number, -- not present in JAG source; domain_model requires NOT NULL, default to 1
       ts.nominal_apparent_power,
       NULL::integer                            AS max_apparent_power, -- no distinct source attribute observed
       ts.max_apparent_power_factor
FROM model_equipment e
         JOIN jag2nsc_equipment_class ec ON ec.equipment_id = e.id
         LEFT JOIN jag2nsc_transformer_satellite ts ON ts.equipment_id = e.id
WHERE ec.cim_class = 'PowerTransformer';

CREATE VIEW busbar AS
SELECT jag2nsc_id(bc.id)         AS id,
       jag2nsc_id(bc.parent_id)   AS container_id
FROM model_container bc
WHERE bc.type = 'busbar';

CREATE VIEW house_connection AS
SELECT jag2nsc_id(hc.id)                                   AS id,
       COALESCE(loc.address, n.name, hc.id)::varchar(255)   AS address, -- prefer the Meter's UsagePointLocation.mainAddress satellite, fall back to name/id
       g.lat                                                 AS latitude,
       g.lon                                                 AS longitude,
       COALESCE(up.rated_power, 0)                           AS rated_power, -- from the house's Meter's UsagePoint.ratedPower satellite, else 0
       COALESCE(up.malo, hc.id)                              AS malo, -- from UsagePoint.marketParticipantLocationIdentifier, else external_id as stand-in
       hc.id                                                 AS network_area_id -- placeholder: no network-area data in JAG source
FROM model_container hc
         LEFT JOIN jag2nsc_display_name n ON n.owner_id = hc.id
         LEFT JOIN model_geometry g ON g.owner_id = hc.id AND g.owner_kind = 'container'
         LEFT JOIN LATERAL (
    SELECT max((s.attrs ->> 'UsagePoint.ratedPower')::double precision)               AS rated_power,
           max(s.attrs ->> 'UsagePoint.marketParticipantLocationIdentifier')          AS malo
    FROM model_equipment eq
             JOIN jag2nsc_satellite s ON s.owner_id = eq.id AND s.class = 'UsagePoint'
    WHERE eq.container_id = hc.id
    ) up ON TRUE
         LEFT JOIN LATERAL (
    SELECT max(s.attrs ->> 'Location.mainAddress') AS address
    FROM model_equipment eq
             JOIN jag2nsc_satellite s ON s.owner_id = eq.id AND s.class = 'UsagePointLocation'
    WHERE eq.container_id = hc.id
    ) loc ON TRUE
WHERE hc.type = 'house';

CREATE VIEW network_device AS
SELECT id, NULL::varchar(64) AS external_id, NULL::varchar(255) AS name, NULL::bigint AS net_island, NULL::bigint AS network_group_id FROM transformer
UNION ALL
SELECT id, NULL, NULL, NULL, NULL FROM busbar
UNION ALL
SELECT id, NULL, NULL, NULL, NULL FROM house_connection;
-- external_id/name/net_island/network_group_id are intentionally left NULL at the base-table
-- level here to avoid re-deriving them redundantly; join back to container/model_container
-- via the subtype view's id if you need the external_id (net_island/network_group are
-- persistor/connector-only concepts with no JAG source data at all, see cim-mapping.md §6).

-- ---------------------------------------------------------------------------------------
-- 4. feeder_area  <=  model_container rows of type 'bay'
--    (`Feeder` is CIM/NSC's own synonym for `Bay`, see copilot-instructions.md)
-- ---------------------------------------------------------------------------------------
CREATE VIEW feeder_area AS
SELECT jag2nsc_id(bc.id) AS id,
       bc.id              AS external_id,
       n.name,
       NULL::bigint       AS net_island,
       NULL::bigint       AS network_group_id
FROM model_container bc
         LEFT JOIN jag2nsc_display_name n ON n.owner_id = bc.id
WHERE bc.type = 'bay';

-- ---------------------------------------------------------------------------------------
-- 5. terminal  <=  synthesized from model_edge (JAG's two-terminal/"Zweipol" equipment
--    model has no explicit Terminal entity; domain_model's `terminal` table is derived by
--    materializing exactly the two connection points every edge already implies).
--
-- Every model_edge row (one per equipment) yields exactly two terminal rows,
-- `<equipment_id>#T1` / `<equipment_id>#T2`.
--
-- terminal.network_device_id is populated when:
--   * the equipment IS a network device itself (PowerTransformer), or
--   * the equipment lives directly inside a `house` container (=> that house_connection)
-- terminal.feeder_area_id is populated when the equipment lives directly inside a `bay`
-- container and is not already tied to a network device.
-- Equipment sitting directly in a `substation`/`distribution-box` container without being
-- itself a network device (e.g. a stray protection fuse at station level), or inside an
-- `acline` chain, gets neither - it is a valid, "detached" terminal (domain_model relaxes
-- this via V32's CHECK constraint), only missing a device/feeder-area anchor.
--
-- Busbars: a `BusbarSection` equipment has no `model_edge` row (no two-terminal wiring),
-- but when the source carries a `busbar_node_id` attribute (present in the
-- example_as_cim.pg_dump reference dataset, absent in some larger synthetic ones - a
-- data-availability gap, not a design gap) every BusbarSection sibling inside the same
-- busbar container repeats the same node id. We pick the single "bare" (non `#`-suffixed)
-- sibling equipment as the terminal's stand-in equipment_id (verified 1:1 per busbar
-- container) and synthesize exactly one terminal (`<id>#T1`) for it - see the dedicated
-- refresh function/triggers below.
--
-- PERFORMANCE NOTE: `node_id` is derived from `model_edge.terminal1_node_id` /
-- `.terminal2_node_id`, which have no index of their own, and connection-building (§6)
-- needs a self-join keyed on exactly that column across all ~2x|model_edge| terminal
-- instances. A plain VIEW re-derives and re-joins this on every query with no usable
-- index, which does not scale (verified: minutes, not seconds, on a ~85k-equipment
-- dataset). `jag2nsc_terminal_idx` is therefore a slim, indexed, trigger-synced TABLE
-- (not a view) holding only the join keys - never business data - kept in lockstep with
-- `model_edge`/`model_equipment`/`model_attribute(cim_class)` by AFTER-triggers below, so
-- changes to `jag` are reflected within the same transaction (no batch job, no polling).
-- All consumer-facing objects (§2-§7) remain plain, always-current VIEWS on top of it.
-- ---------------------------------------------------------------------------------------
CREATE TABLE jag2nsc_terminal_idx
(
    terminal_key text PRIMARY KEY,
    equipment_id text NOT NULL,
    terminal_no  smallint NOT NULL,
    node_id      text NOT NULL,
    container_id text NOT NULL,
    cim_class    text
);
CREATE INDEX idx_jag2nsc_terminal_idx_node ON jag2nsc_terminal_idx (node_id);
CREATE INDEX idx_jag2nsc_terminal_idx_equipment ON jag2nsc_terminal_idx (equipment_id);
CREATE INDEX idx_jag2nsc_terminal_idx_container ON jag2nsc_terminal_idx (container_id);

CREATE OR REPLACE FUNCTION jag2nsc_terminal_idx_refresh(p_equipment_id text) RETURNS void
    LANGUAGE sql AS
$$
DELETE FROM jag2nsc_terminal_idx WHERE equipment_id = p_equipment_id;
INSERT INTO jag2nsc_terminal_idx (terminal_key, equipment_id, terminal_no, node_id, container_id, cim_class)
SELECT p_equipment_id || '#T1', p_equipment_id, 1, e.terminal1_node_id, m.container_id, ec.cim_class
FROM model_edge e
         JOIN model_equipment m ON m.id = e.equipment_id
         LEFT JOIN jag2nsc_equipment_class ec ON ec.equipment_id = e.equipment_id
WHERE e.equipment_id = p_equipment_id
UNION ALL
SELECT p_equipment_id || '#T2', p_equipment_id, 2, e.terminal2_node_id, m.container_id, ec.cim_class
FROM model_edge e
         JOIN model_equipment m ON m.id = e.equipment_id
         LEFT JOIN jag2nsc_equipment_class ec ON ec.equipment_id = e.equipment_id
WHERE e.equipment_id = p_equipment_id;
$$;

-- Keep the index table in sync with model_edge (the edge/its two node ids changing).
CREATE OR REPLACE FUNCTION jag2nsc_trg_sync_model_edge() RETURNS trigger
    LANGUAGE plpgsql AS
$$
BEGIN
    IF TG_OP = 'DELETE' THEN
        DELETE FROM jag2nsc_terminal_idx WHERE equipment_id = OLD.equipment_id;
        RETURN OLD;
    END IF;
    PERFORM jag2nsc_terminal_idx_refresh(NEW.equipment_id);
    RETURN NEW;
END;
$$;
CREATE TRIGGER trg_jag2nsc_sync_model_edge
    AFTER INSERT OR UPDATE ON model_edge
    FOR EACH ROW
EXECUTE FUNCTION jag2nsc_trg_sync_model_edge();
CREATE TRIGGER trg_jag2nsc_sync_model_edge_delete
    AFTER DELETE
    ON model_edge
    FOR EACH ROW
EXECUTE FUNCTION jag2nsc_trg_sync_model_edge();

-- Keep it in sync with model_equipment (container reassignment) too.
CREATE OR REPLACE FUNCTION jag2nsc_trg_sync_model_equipment() RETURNS trigger
    LANGUAGE plpgsql AS
$$
BEGIN
    IF TG_OP = 'DELETE' THEN
        DELETE FROM jag2nsc_terminal_idx WHERE equipment_id = OLD.id;
        RETURN OLD;
    END IF;
    IF EXISTS (SELECT 1 FROM model_edge WHERE equipment_id = NEW.id) THEN
        PERFORM jag2nsc_terminal_idx_refresh(NEW.id);
    ELSIF EXISTS (SELECT 1 FROM model_attribute WHERE owner_id = NEW.id AND key = 'busbar_node_id') THEN
        PERFORM jag2nsc_terminal_idx_refresh_busbar(NEW.id);
    END IF;
    RETURN NEW;
END;
$$;
CREATE TRIGGER trg_jag2nsc_sync_model_equipment
    AFTER INSERT OR UPDATE OR DELETE
    ON model_equipment
    FOR EACH ROW
EXECUTE FUNCTION jag2nsc_trg_sync_model_equipment();

-- Keep it in sync with model_attribute's `cim_class` key (changes the derived terminal
-- type / network_device_id resolution for that one equipment only).
CREATE OR REPLACE FUNCTION jag2nsc_trg_sync_model_attribute_cim_class() RETURNS trigger
    LANGUAGE plpgsql AS
$$
DECLARE
    v_owner text := COALESCE(NEW.owner_id, OLD.owner_id);
BEGIN
    IF EXISTS (SELECT 1 FROM model_edge WHERE equipment_id = v_owner) THEN
        PERFORM jag2nsc_terminal_idx_refresh(v_owner);
    END IF;
    RETURN COALESCE(NEW, OLD);
END;
$$;
CREATE TRIGGER trg_jag2nsc_sync_model_attribute_cim_class
    AFTER INSERT OR UPDATE
    ON model_attribute
    FOR EACH ROW
    WHEN (NEW.key = 'cim_class')
EXECUTE FUNCTION jag2nsc_trg_sync_model_attribute_cim_class();
CREATE TRIGGER trg_jag2nsc_sync_model_attribute_cim_class_delete
    AFTER DELETE
    ON model_attribute
    FOR EACH ROW
    WHEN (OLD.key = 'cim_class')
EXECUTE FUNCTION jag2nsc_trg_sync_model_attribute_cim_class();

-- Busbar-terminal synthesis (see comment above jag2nsc_terminal_idx): re-derives the single
-- `#T1` terminal row for one "bare" BusbarSection equipment from its `busbar_node_id`
-- attribute. Delete-then-reinsert is idempotent for INSERT/UPDATE/DELETE alike - after a
-- DELETE the re-SELECT simply finds nothing and leaves the row removed.
CREATE OR REPLACE FUNCTION jag2nsc_terminal_idx_refresh_busbar(p_equipment_id text) RETURNS void
    LANGUAGE sql AS
$$
DELETE FROM jag2nsc_terminal_idx WHERE equipment_id = p_equipment_id AND cim_class = 'BusbarSection';
INSERT INTO jag2nsc_terminal_idx (terminal_key, equipment_id, terminal_no, node_id, container_id, cim_class)
SELECT p_equipment_id || '#T1', p_equipment_id, 1, jag2nsc_attr_text(a.value), m.container_id, 'BusbarSection'
FROM model_attribute a
         JOIN model_equipment m ON m.id = a.owner_id
WHERE a.owner_id = p_equipment_id
  AND a.key = 'busbar_node_id'
  AND a.seq = 0
  AND position('#' IN p_equipment_id) = 0 -- only the "bare" representative equipment per busbar
  AND NOT EXISTS (SELECT 1 FROM model_edge WHERE equipment_id = p_equipment_id);
$$;

CREATE OR REPLACE FUNCTION jag2nsc_trg_sync_model_attribute_busbar_node() RETURNS trigger
    LANGUAGE plpgsql AS
$$
DECLARE
    v_owner text := COALESCE(NEW.owner_id, OLD.owner_id);
BEGIN
    PERFORM jag2nsc_terminal_idx_refresh_busbar(v_owner);
    RETURN COALESCE(NEW, OLD);
END;
$$;
CREATE TRIGGER trg_jag2nsc_sync_model_attribute_busbar_node
    AFTER INSERT OR UPDATE
    ON model_attribute
    FOR EACH ROW
    WHEN (NEW.key = 'busbar_node_id')
EXECUTE FUNCTION jag2nsc_trg_sync_model_attribute_busbar_node();
CREATE TRIGGER trg_jag2nsc_sync_model_attribute_busbar_node_delete
    AFTER DELETE
    ON model_attribute
    FOR EACH ROW
    WHEN (OLD.key = 'busbar_node_id')
EXECUTE FUNCTION jag2nsc_trg_sync_model_attribute_busbar_node();

-- One-off backfill for rows that already existed before the triggers were installed.
INSERT INTO jag2nsc_terminal_idx (terminal_key, equipment_id, terminal_no, node_id, container_id, cim_class)
SELECT e.equipment_id || '#T1', e.equipment_id, 1, e.terminal1_node_id, m.container_id, ec.cim_class
FROM model_edge e
         JOIN model_equipment m ON m.id = e.equipment_id
         LEFT JOIN jag2nsc_equipment_class ec ON ec.equipment_id = e.equipment_id
UNION ALL
SELECT e.equipment_id || '#T2', e.equipment_id, 2, e.terminal2_node_id, m.container_id, ec.cim_class
FROM model_edge e
         JOIN model_equipment m ON m.id = e.equipment_id
         LEFT JOIN jag2nsc_equipment_class ec ON ec.equipment_id = e.equipment_id
UNION ALL
SELECT a.owner_id || '#T1', a.owner_id, 1, jag2nsc_attr_text(a.value), m.container_id, 'BusbarSection'
FROM model_attribute a
         JOIN model_equipment m ON m.id = a.owner_id
WHERE a.key = 'busbar_node_id'
  AND a.seq = 0
  AND position('#' IN a.owner_id) = 0
  AND NOT EXISTS (SELECT 1 FROM model_edge WHERE equipment_id = a.owner_id);

CREATE VIEW jag2nsc_terminal_raw AS
SELECT terminal_key, equipment_id, terminal_no, node_id
FROM jag2nsc_terminal_idx;

CREATE VIEW jag2nsc_terminal AS
SELECT r.terminal_key,
       r.equipment_id,
       r.terminal_no,
       r.node_id,
       ec.cim_class,
       c.type                                                                     AS container_type,
       CASE
           WHEN ec.cim_class = 'PowerTransformer' THEN jag2nsc_id(r.equipment_id)
           WHEN ec.cim_class = 'BusbarSection' THEN jag2nsc_id(c.id)
           WHEN c.type = 'house' THEN jag2nsc_id(c.id)
           END                                                                     AS network_device_id,
       CASE
           WHEN ec.cim_class <> 'PowerTransformer' AND c.type = 'bay' THEN jag2nsc_id(c.id)
           END                                                                     AS feeder_area_id,
       CASE ec.cim_class
           WHEN 'Fuse' THEN 'FUSE'
           WHEN 'Switch' THEN 'SWITCH'
           ELSE 'NOT_SWITCHABLE'
           END::terminal_type                                                     AS type,
       fuse.nominal_current,
       eg.lat,
       eg.lon
FROM jag2nsc_terminal_raw r
         JOIN jag2nsc_equipment_class ec ON ec.equipment_id = r.equipment_id
         JOIN model_equipment eq ON eq.id = r.equipment_id
         JOIN model_container c ON c.id = eq.container_id
         LEFT JOIN jag2nsc_equipment_geometry eg ON eg.equipment_id = r.equipment_id
         LEFT JOIN (
    SELECT owner_id AS equipment_id, jag2nsc_attr_text(value)::integer AS nominal_current
    FROM model_attribute
    WHERE key = 'Fuse.nominalCurrent'
      AND seq = 0
    ) fuse ON fuse.equipment_id = r.equipment_id;

CREATE VIEW terminal AS
SELECT jag2nsc_id(t.terminal_key) AS id,
       t.terminal_key              AS external_id,
       n.name,
       t.network_device_id,
       t.feeder_area_id,
       t.type,
       TRUE                        AS default_switching_state,
       t.nominal_current           AS fuse_value, -- JAG only carries one current rating per fuse; reused for both columns
       t.nominal_current           AS fuse_nominal_current,
       t.lat                        AS latitude,
       t.lon                        AS longitude
FROM jag2nsc_terminal t
         LEFT JOIN jag2nsc_display_name n ON n.owner_id = t.equipment_id;

-- ---------------------------------------------------------------------------------------
-- 6. connection / connection_terminal_map  <=  JAG Nodes shared by >=2 terminal-instances
--
-- IMPORTANT (revised after live testing against a ~85k-equipment dataset): a JAG Node is
-- NOT usually shared by exactly two terminal-instances - T-taps/branch points (e.g. a
-- house service tap off a running cable) commonly have degree 3, and true busbar-like
-- collector points can have much higher degree (11+ terminals converging on one Node was
-- observed). domain_model already has a construct for exactly this: `connection` holds one
-- representative pair of terminals (source/target, both NOT NULL columns), while
-- `connection_terminal_map` is documented in schema.md as "the M:N join table for
-- multi-terminal paths" - i.e. it, not `connection` alone, is the authoritative membership
-- list for a node with more than two terminal-instances. This mapping therefore creates
-- exactly one `connection` row per JAG Node with degree >= 2 (excluding `GND`, the virtual
-- earthing sink - a device whose second terminal is GND is legitimately a dead-end with no
-- partner), with ALL of that node's terminal-instances enumerated in
-- `connection_terminal_map` (sequence_number = position in a deterministic, terminal_key-
-- sorted order). `connection.source_id`/`.target_id` are simply the first two members of
-- that same ordering - a representative pair, not an exhaustive one; read
-- `connection_terminal_map` for the full node membership.
-- ---------------------------------------------------------------------------------------
CREATE VIEW jag2nsc_node_members AS
SELECT node_id,
       array_agg(terminal_key ORDER BY terminal_key) AS terminal_keys,
       count(*)                                       AS degree
FROM jag2nsc_terminal_idx
WHERE node_id <> 'GND'
GROUP BY node_id
HAVING count(*) >= 2;

CREATE VIEW connection AS
SELECT jag2nsc_id(m.node_id)                AS id,
       m.node_id                             AS external_id,
       jag2nsc_id(m.terminal_keys[1])        AS source_id,
       CASE
           WHEN st.cim_class = 'PowerTransformer' THEN 'TRANSFORMER'
           WHEN st.cim_class = 'BusbarSection' THEN 'BUSBAR'
           WHEN sc.type = 'house' THEN 'HOUSE_CONNECTION'
           END::device_type                  AS source_device_type,
       jag2nsc_id(m.terminal_keys[2])        AS target_id,
       CASE
           WHEN tt.cim_class = 'PowerTransformer' THEN 'TRANSFORMER'
           WHEN tt.cim_class = 'BusbarSection' THEN 'BUSBAR'
           WHEN tc.type = 'house' THEN 'HOUSE_CONNECTION'
           END::device_type                  AS target_device_type,
       TRUE                                   AS is_active
FROM jag2nsc_node_members m
         JOIN jag2nsc_terminal_idx st ON st.terminal_key = m.terminal_keys[1]
         JOIN jag2nsc_terminal_idx tt ON tt.terminal_key = m.terminal_keys[2]
         LEFT JOIN model_container sc ON sc.id = st.container_id
         LEFT JOIN model_container tc ON tc.id = tt.container_id;
-- source_device_type/target_device_type now resolve TRANSFORMER, BUSBAR (via the
-- busbar_node_id-derived terminal, when present in the source) and HOUSE_CONNECTION;
-- still NULL for a bare feeder-area/bay terminal with no network-device anchor, matching
-- domain_model's own nullable FK semantics for that case (cim-mapping.md §6).

CREATE VIEW connection_terminal_map AS
SELECT jag2nsc_id(m.node_id || '~' || u.tk) AS id,
       jag2nsc_id(m.node_id)                 AS connection_id,
       jag2nsc_id(u.tk)                      AS terminal_id,
       u.ord::integer                        AS sequence_number
FROM jag2nsc_node_members m,
     LATERAL unnest(m.terminal_keys) WITH ORDINALITY AS u (tk, ord);

-- ---------------------------------------------------------------------------------------
-- 7. line_segment  <=  equipment with cim_class = 'ACLineSegment'
--
-- Every ACLineSegment's two terminal-instances (#T1/#T2) sit on two different Nodes, so it
-- is naturally a member of two different `connection` rows (one per side) instead of
-- sitting purely as cable metadata on a single connection between its two real neighbours.
-- This differs from the real connector, which can chain several contiguous ACLineSegments
-- into ONE connection with several ordered line_segment rows (see README.md "Known
-- limitations") - the resulting topology is still electrically equivalent, just modeled as
-- more, shorter connections chained through intermediate node-terminals instead of fewer,
-- longer ones. `DISTINCT ON (equipment_id)` deterministically keeps exactly ONE of the two
-- flanking connections per ACLineSegment, so its length/r is never double-counted.
-- ---------------------------------------------------------------------------------------
CREATE VIEW line_segment AS
SELECT DISTINCT ON (t.equipment_id)
       jag2nsc_id(t.node_id || '~seg~' || t.equipment_id) AS id,
       jag2nsc_id(t.node_id)                               AS connection_id,
       t.equipment_id                                       AS external_id,
       1                                                     AS sequence_number,
       len.value                                             AS length,
       r.value                                               AS r,
       NULL::double precision                                AS x,
       NULL::double precision                                AS bch,
       NULL::double precision                                AS total_resistance,
       pos.path_positions                                    AS path_positions
FROM jag2nsc_terminal_idx t
         JOIN jag2nsc_node_members m ON m.node_id = t.node_id
         LEFT JOIN (SELECT owner_id, jag2nsc_attr_text(value)::double precision AS value
                    FROM model_attribute WHERE key = 'Conductor.length' AND seq = 0) len ON len.owner_id = t.equipment_id
         LEFT JOIN (SELECT owner_id, jag2nsc_attr_text(value)::double precision AS value
                    FROM model_attribute WHERE key = 'ACLineSegment.r' AND seq = 0) r ON r.owner_id = t.equipment_id
         LEFT JOIN LATERAL (
    SELECT jsonb_agg(jsonb_build_object(
                   'sequenceNumber', (s.attrs ->> 'PositionPoint.sequenceNumber')::integer,
                   'x', (s.attrs ->> 'PositionPoint.xPosition')::double precision,
                   'y', (s.attrs ->> 'PositionPoint.yPosition')::double precision
           ) ORDER BY (s.attrs ->> 'PositionPoint.sequenceNumber')::integer) AS path_positions
    FROM jag2nsc_satellite s
    WHERE s.owner_id = t.equipment_id AND s.class = 'PositionPoint'
    ) pos ON TRUE
WHERE t.cim_class = 'ACLineSegment'
ORDER BY t.equipment_id, t.node_id;
-- path_positions is a best-effort JSONB array of {sequenceNumber, x, y} built from
-- PositionPoint satellites - domain_model's column has no fixed shape/schema constraint
-- (plain JSONB), so this is a compatible, additive population rather than a guess.

-- ---------------------------------------------------------------------------------------
-- 8. steua / steua_step / measuring_device / terminal_feeder_end_extension
--
-- These four tables were originally assumed unfillable ("no CIM/JAG source"). Closer
-- inspection of JAG's generic `model_attribute.key='satellite'` JSON payloads (see
-- jag2nsc_satellite above) showed real, usable source data after all:
--   - PowerElectronicsConnection equipment optionally carries a self-satellite of one of
--     the concrete PowerElectronicsUnit subtypes (AirConditioningUnit/Heatpump/BatteryUnit/
--     PhotoVoltaicUnit - jag2nsc_steua_unit) plus a RegulatingControl self-satellite
--     (steua eligibility + control-type/min/max) and, for some, a DiscreteControlLimit
--     self-satellite per step (steua_step).
--   - Meter equipment optionally carries a UsagePoint self-satellite (melo, rated power -
--     already used above for house_connection) and both Meter/PEC live directly inside the
--     `house` container, so the existing terminal->house_connection resolution logic
--     (jag2nsc_terminal, `c.type = 'house'`) is reused unchanged for the FK.
--
-- KNOWN LIMITATIONS (documented in README.md):
--   - PhotoVoltaicUnit has no matching domain_model `steua_type` enum value; left NULL,
--     with `steua_role = 'PRODUCER'` as the best-effort distinguishing signal instead
--     (all other subtypes default to 'CONSUMER').
--   - `measuring_interval`/`transmission_interval`/`sign_convention` (measuring_device) and
--     `sequence_number`/`first_segment_cable_type` (terminal_feeder_end_extension) have no
--     JAG source at all and are always NULL here - real (NOT NULL in some cases) operator
--     configuration in domain_model, out of CIM/JAG scope by design.
-- ---------------------------------------------------------------------------------------
CREATE VIEW steua AS
SELECT jag2nsc_id(eq.id)                                                          AS id,
       eq.id                                                                       AS external_id,
       n.name,
       NULL::bigint                                                                AS net_island,
       NULL::bigint                                                                AS network_group_id,
       jag2nsc_id(c.id)                                                            AS house_connection_id,
       CASE u.unit_class
           WHEN 'AirConditioningUnit' THEN 'AIRCONDITION'
           WHEN 'Heatpump' THEN 'HEATPUMP'
           WHEN 'BatteryUnit' THEN 'STORAGE'
           END::steua_type                                                        AS steua_type,
       CASE WHEN u.unit_class = 'PhotoVoltaicUnit' THEN 'PRODUCER' ELSE 'CONSUMER' END::steua_role AS steua_role,
       (u.attrs ->> 'PowerElectronicsUnit.maxP')::double precision                  AS max_power,
       NULL::integer                                                               AS total_controllable_load, -- no JAG source, NOT NULL in real table
       CASE WHEN (rc.attrs ->> 'RegulatingControl.discrete')::boolean
                THEN 'STEPPED' ELSE 'STEPLESS' END::steua_control_type             AS steua_control_type,
       CASE WHEN (rc.attrs ->> 'RegulatingControl.discrete')::boolean IS FALSE
                THEN (rc.attrs ->> 'RegulatingControl.minAllowedTargetValue')::double precision END AS stepless_min,
       CASE WHEN (rc.attrs ->> 'RegulatingControl.discrete')::boolean IS FALSE
                THEN (rc.attrs ->> 'RegulatingControl.maxAllowedTargetValue')::double precision END AS stepless_max
FROM model_equipment eq
         JOIN jag2nsc_equipment_class ec ON ec.equipment_id = eq.id AND ec.cim_class = 'PowerElectronicsConnection'
         JOIN jag2nsc_steua_unit u ON u.equipment_id = eq.id
         JOIN model_container c ON c.id = eq.container_id AND c.type = 'house'
         LEFT JOIN jag2nsc_display_name n ON n.owner_id = eq.id
         LEFT JOIN jag2nsc_satellite rc ON rc.owner_id = eq.id AND rc.class = 'RegulatingControl';

CREATE VIEW steua_step AS
SELECT jag2nsc_id(s.owner_id || '~step~' || s.seq) AS id,
       jag2nsc_id(s.owner_id)                        AS steua_id,
       (s.attrs ->> 'DiscreteControlLimit.value')::double precision AS step_value
FROM jag2nsc_satellite s
WHERE s.class = 'DiscreteControlLimit';

CREATE VIEW measuring_device AS
SELECT DISTINCT ON (t.equipment_id)
       jag2nsc_id(t.equipment_id)  AS id,
       t.equipment_id               AS external_id,
       n.name,
       jag2nsc_id(t.terminal_key)   AS terminal_id,
       NULL::bigint                 AS measuring_interval,      -- no JAG source, see limitations above
       NULL::bigint                 AS transmission_interval,   -- no JAG source, see limitations above
       up.attrs ->> 'UsagePoint.marketParticipantLocationIdentifier' AS melo,
       NULL::sign_convention        AS sign_convention          -- no JAG source, see limitations above
FROM jag2nsc_terminal_idx t
         JOIN jag2nsc_equipment_class ec ON ec.equipment_id = t.equipment_id AND ec.cim_class = 'Meter'
         LEFT JOIN jag2nsc_display_name n ON n.owner_id = t.equipment_id
         LEFT JOIN jag2nsc_satellite up ON up.owner_id = t.equipment_id AND up.class = 'UsagePoint'
ORDER BY t.equipment_id, t.terminal_no;

CREATE VIEW terminal_feeder_end_extension AS
SELECT id,
       NULL::bigint  AS sequence_number,           -- no JAG source, see limitations above
       NULL::varchar(64) AS first_segment_cable_type -- no JAG source, see limitations above
FROM terminal
WHERE feeder_area_id IS NOT NULL;

COMMIT;

