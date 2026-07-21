-- jag2nsc: read-only access role for consumers of the domain_model-shaped views.
--
-- Defense in depth (three independent layers, any one of which alone would already
-- prevent writes from a view consumer):
--   1. Every view in views.sql involves a JOIN/UNION/aggregate, so none of them satisfy
--      PostgreSQL's "simple view" rule for automatic updatability - INSERT/UPDATE/DELETE
--      against ANY of them is rejected by Postgres itself, regardless of role grants.
--   2. This role is granted SELECT on the mapped views ONLY - never on model_*/staging_*
--      base tables, and never INSERT/UPDATE/DELETE/TRUNCATE/REFERENCES/TRIGGER on
--      anything. (A view's underlying-table access uses the VIEW OWNER's privileges, not
--      the querying role's - so the reader role does not need, and is not given, any
--      privilege on model_*/staging_* at all.)
--   3. `default_transaction_read_only = on` is set on the role itself, so even a
--      misconfigured future GRANT could not be exploited to write - every session opened
--      as this role starts in a read-only transaction state.
--
-- Usage:
--   psql -f reader_role.sql                         -- (re)creates the role + grants
--   ALTER ROLE jag2nsc_reader WITH PASSWORD '...';   -- set/rotate the login secret out-of-band
--
-- Re-run this script after every re-application of views.sql that adds/removes views -
-- GRANT statements are NOT automatically extended to new views.

DO
$$
    BEGIN
        IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'jag2nsc_reader') THEN
            CREATE ROLE jag2nsc_reader WITH LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOREPLICATION
                CONNECTION LIMIT 10;
        END IF;
    END
$$;

ALTER ROLE jag2nsc_reader SET default_transaction_read_only = on;

-- Defensive baseline: strip any privileges this role might already hold anywhere in the
-- public schema (in case it pre-existed with broader grants), then re-grant only what is
-- intended below.
REVOKE ALL PRIVILEGES ON ALL TABLES IN SCHEMA public FROM jag2nsc_reader;
REVOKE ALL PRIVILEGES ON SCHEMA public FROM jag2nsc_reader;

GRANT USAGE ON SCHEMA public TO jag2nsc_reader;

GRANT SELECT ON
    container,
    network_device,
    transformer,
    busbar,
    house_connection,
    feeder_area,
    terminal,
    connection,
    connection_terminal_map,
    line_segment,
    measuring_device,
    steua,
    steua_step,
    terminal_feeder_end_extension,
    threshold,
    threshold_history,
    switching_state,
    switching_state_history,
    circuit_config,
    circuit_config_history,
    sensitivity,
    sensitivity_history
    TO jag2nsc_reader;

-- Explicitly NOT granted to jag2nsc_reader (must stay that way):
--   * any model_*/staging_*/import_flag base table (no SELECT, no writes)
--   * INSERT/UPDATE/DELETE/TRUNCATE/REFERENCES/TRIGGER on anything, including the views above
