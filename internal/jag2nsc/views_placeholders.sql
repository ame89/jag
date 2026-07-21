-- views_placeholders.sql - stand-in views for the eight domain_model tables jag2nsc does not
-- (yet) have a full CIM/JAG raw data source for:
--   threshold / threshold_history
--   switching_state / switching_state_history
--   circuit_config / circuit_config_history
--   sensitivity / sensitivity_history
--
-- threshold/threshold_history are NOT empty: they reproduce the exact 3 static SYSTEM
-- default rows (terminal_id IS NULL, is_default = true) that domain_model's own
-- V1__Init.sql Flyway migration seeds into EVERY domain_model database unconditionally -
-- verified live against the real config-persistor-owned compose-domain-model-db-1
-- (SELECT * FROM threshold / threshold_history both return exactly these 3 rows,
-- byte-for-byte). Since these rows are static seed data, not derived from any CIM/network
-- content, hardcoding them here is a faithful mirror, not a stub.
--
-- The other six views (switching_state/switching_state_history, circuit_config/
-- circuit_config_history, sensitivity/sensitivity_history) remain permanently EMPTY - this
-- exists purely so a consumer querying jag2nsc "as if" it were domain_model gets a
-- structurally correct, empty result set (same column names/types as the real table)
-- instead of an SQL error ("relation does not exist"). No jag2nsc_* backing table/Go
-- computation exists for any of these - they always return zero rows.
--
-- Status (see README.md "Known limitations" / plan.md for details):
--   * threshold/threshold_history: static SYSTEM-default rows reproduced (see above);
--     per-terminal, non-default rows (is_default = false, terminal_id set) are still TODO -
--     no CIM raw source identified yet for warning/critical/load-limitation values.
--   * switching_state/switching_state_history: a raw JAG data source HAS been identified
--     (Switch.normalOpen) but no Go computation has been implemented yet - TODO, tracked as
--     an open item.
--   * circuit_config/circuit_config_history, sensitivity/sensitivity_history: genuinely OUT
--     OF SCOPE - these are not CIM/topology data at all (circuit_config is operator-set
--     runtime configuration; sensitivity is computed by the separate
--     org-openk-nsc-sensitivity-calculator service from live telemetry, not from the
--     network model), so there is no raw source in a JAG database to ever derive them from.
--
-- Enum types mirror domain_model's own Postgres enums (see views.sql's own enum block for
-- the established pattern) so column types match exactly even though no row ever exists.
--
-- Idempotent: safe to re-run (DROP ... CASCADE then CREATE), same convention as views.sql.

BEGIN;

DROP VIEW IF EXISTS sensitivity_history CASCADE;
DROP VIEW IF EXISTS sensitivity CASCADE;
DROP VIEW IF EXISTS circuit_config_history CASCADE;
DROP VIEW IF EXISTS circuit_config CASCADE;
DROP VIEW IF EXISTS switching_state_history CASCADE;
DROP VIEW IF EXISTS switching_state CASCADE;
DROP VIEW IF EXISTS threshold_history CASCADE;
DROP VIEW IF EXISTS threshold CASCADE;

DROP TYPE IF EXISTS threshold_type CASCADE;
DROP TYPE IF EXISTS load_limitation_mode CASCADE;
DROP TYPE IF EXISTS sensitivity_calculation_trigger_type CASCADE;

CREATE TYPE threshold_type AS ENUM ('OVERLOAD', 'UNDERVOLTAGE', 'OVERVOLTAGE');
CREATE TYPE load_limitation_mode AS ENUM ('MANUAL', 'SEMIAUTOMATIC', 'AUTOMATIC');
CREATE TYPE sensitivity_calculation_trigger_type AS ENUM ('MODEL_CHANGE', 'SWITCH_STATE_CHANGE');

-- threshold / threshold_history -----------------------------------------------------------
-- These are NOT empty: domain_model's own V1__Init.sql Flyway migration seeds exactly these
-- 3 system-default rows (terminal_id IS NULL, is_default = true) into EVERY domain_model
-- database unconditionally - verified live against the real config-persistor-owned
-- compose-domain-model-db-1 (SELECT * FROM threshold / threshold_history both return
-- exactly these 3 rows, byte-for-byte). They are static seed data, not derived from any
-- CIM/network content, so hardcoding them here is a faithful mirror rather than a stub.
-- TODO (open item, not yet implemented): per-terminal, non-default threshold rows (is_default
-- = false, terminal_id set) - no CIM raw source identified yet for warning/critical/load-
-- limitation values themselves, so those rows are still not reproduced here.
CREATE VIEW threshold AS
SELECT * FROM (VALUES
    (1::bigint, NULL::bigint, TIMESTAMPTZ '2020-01-01 00:00:00+00', 'SYSTEM'::varchar(255), true, 'OVERLOAD'::threshold_type, 0.8::double precision, NULL::double precision, 0.9::double precision, NULL::double precision, 0.85::double precision, NULL::double precision),
    (2::bigint, NULL::bigint, TIMESTAMPTZ '2020-01-01 00:00:00+00', 'SYSTEM'::varchar(255), true, 'OVERVOLTAGE'::threshold_type, 1.05::double precision, NULL::double precision, 1.08::double precision, NULL::double precision, 1.065::double precision, NULL::double precision),
    (3::bigint, NULL::bigint, TIMESTAMPTZ '2020-01-01 00:00:00+00', 'SYSTEM'::varchar(255), true, 'UNDERVOLTAGE'::threshold_type, 0.95::double precision, NULL::double precision, 0.92::double precision, NULL::double precision, 0.935::double precision, NULL::double precision)
) AS t(id, terminal_id, modified_date, modified_by, is_default, threshold_type, warning_fraction, warning_value, critical_fraction, critical_value, load_limitation_target_fraction, load_limitation_target_value);

CREATE VIEW threshold_history AS
SELECT * FROM (VALUES
    (1::bigint, NULL::bigint, TIMESTAMPTZ '2020-01-01 00:00:00+00', 'SYSTEM'::varchar(255), true, 'OVERLOAD'::threshold_type, 0.8::double precision, NULL::double precision, 0.9::double precision, NULL::double precision, 0.85::double precision, NULL::double precision),
    (2::bigint, NULL::bigint, TIMESTAMPTZ '2020-01-01 00:00:00+00', 'SYSTEM'::varchar(255), true, 'OVERVOLTAGE'::threshold_type, 1.05::double precision, NULL::double precision, 1.08::double precision, NULL::double precision, 1.065::double precision, NULL::double precision),
    (3::bigint, NULL::bigint, TIMESTAMPTZ '2020-01-01 00:00:00+00', 'SYSTEM'::varchar(255), true, 'UNDERVOLTAGE'::threshold_type, 0.95::double precision, NULL::double precision, 0.92::double precision, NULL::double precision, 0.935::double precision, NULL::double precision)
) AS t(id, terminal_id, modified_date, modified_by, is_default, threshold_type, warning_fraction, warning_value, critical_fraction, critical_value, load_limitation_target_fraction, load_limitation_target_value);

-- switching_state / switching_state_history ------------------------------------------------
-- TODO (open item, not yet implemented): raw source identified (CIM Switch.normalOpen, read
-- the same way terminal/connection already read model_attribute), but not yet wired up as a
-- Go computation producing a jag2nsc_switching_state table for this view to wrap.
CREATE VIEW switching_state AS
SELECT NULL::bigint       AS id,
       NULL::bigint       AS terminal_id,
       NULL::boolean      AS state,
       NULL::timestamptz  AS modified_date,
       NULL::varchar(255) AS modified_by
WHERE false;

CREATE VIEW switching_state_history AS
SELECT NULL::bigint       AS id,
       NULL::bigint       AS terminal_id,
       NULL::boolean      AS state,
       NULL::timestamptz  AS modified_date,
       NULL::varchar(255) AS modified_by
WHERE false;

-- circuit_config / circuit_config_history ---------------------------------------------------
-- OUT OF SCOPE: operator-set runtime configuration (load_limitation_mode), not CIM/topology
-- data - no raw source in a JAG database will ever populate this.
CREATE VIEW circuit_config AS
SELECT NULL::bigint                AS id,
       NULL::bigint                AS circuit_id,
       NULL::load_limitation_mode  AS load_limitation_mode,
       NULL::timestamptz           AS modified_date,
       NULL::varchar(255)          AS modified_by,
       NULL::varchar(1024)         AS modified_reason
WHERE false;

CREATE VIEW circuit_config_history AS
SELECT NULL::bigint                AS id,
       NULL::bigint                AS circuit_id,
       NULL::load_limitation_mode  AS load_limitation_mode,
       NULL::timestamptz           AS modified_date,
       NULL::varchar(255)          AS modified_by,
       NULL::varchar(1024)         AS modified_reason
WHERE false;

-- sensitivity / sensitivity_history -----------------------------------------------------------
-- OUT OF SCOPE: computed by the separate org-openk-nsc-sensitivity-calculator service from
-- live telemetry, not derivable from the static network model at all.
CREATE VIEW sensitivity AS
SELECT NULL::bigint                                 AS id,
       NULL::sensitivity_calculation_trigger_type    AS calculation_trigger,
       NULL::bigint                                  AS steua_id,
       NULL::bigint                                  AS terminal_id,
       NULL::double precision                        AS sensitivity_power,
       NULL::double precision                        AS sensitivity_voltage,
       NULL::timestamptz                             AS modified_date
WHERE false;

CREATE VIEW sensitivity_history AS
SELECT NULL::bigint                                 AS id,
       NULL::sensitivity_calculation_trigger_type    AS calculation_trigger,
       NULL::bigint                                  AS steua_id,
       NULL::bigint                                  AS terminal_id,
       NULL::double precision                        AS sensitivity_power,
       NULL::double precision                        AS sensitivity_voltage,
       NULL::timestamptz                             AS modified_date
WHERE false;

COMMIT;
