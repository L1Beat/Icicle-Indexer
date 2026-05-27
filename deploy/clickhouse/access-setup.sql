-- Read-only users for the front-end and any dashboard tool.
-- Apply after ClickHouse is up and after the indexer has created the schema.
-- Run with:
--   docker exec -i icicle-clickhouse clickhouse-client --user default --password "$CLICKHOUSE_PASSWORD" < access-setup.sql
--
-- This file is a copy of the canonical anonymous_user.sql at the repo root. Keep them in sync.

CREATE ROLE IF NOT EXISTS frontend_reader;
GRANT SELECT ON default.* TO frontend_reader;
GRANT SHOW ON *.* TO frontend_reader;
GRANT SELECT ON system.parts TO frontend_reader;
GRANT SELECT ON system.tables TO frontend_reader;
GRANT SELECT ON system.columns TO frontend_reader;
GRANT SELECT ON system.databases TO frontend_reader;

DROP SETTINGS PROFILE IF EXISTS anonymous_profile;
CREATE SETTINGS PROFILE anonymous_profile SETTINGS
    readonly = 1,
    allow_ddl = 0,
    allow_introspection_functions = 0,
    max_concurrent_queries_for_user = 10,
    max_threads = 1,
    max_result_rows  = 1000,
    max_result_bytes = 64000000,
    result_overflow_mode = 'break',
    max_rows_to_read  = 1000000,
    max_bytes_to_read = 1000000000,
    max_execution_time = 3,
    max_memory_usage   = 1000000000;

DROP SETTINGS PROFILE IF EXISTS anonymous_heavy_profile;
CREATE SETTINGS PROFILE anonymous_heavy_profile SETTINGS
    readonly = 1,
    allow_ddl = 0,
    allow_introspection_functions = 0,
    max_concurrent_queries_for_user = 2,
    max_rows_to_read  = 0,
    max_bytes_to_read = 0,
    max_partitions_to_read = 0,
    max_result_rows  = 1000,
    max_result_bytes = 64000000,
    result_overflow_mode = 'break',
    max_execution_time = 60,
    max_memory_usage   = 0;

DROP QUOTA IF EXISTS anonymous_quota;
CREATE QUOTA anonymous_quota KEYED BY ip_address
    FOR INTERVAL 1 minute
        MAX QUERIES 4000,
        MAX ERRORS 200;

DROP QUOTA IF EXISTS anonymous_heavy_quota;
CREATE QUOTA anonymous_heavy_quota KEYED BY ip_address
    FOR INTERVAL 1 minute
        MAX QUERIES 10,
        MAX ERRORS 10;

CREATE USER IF NOT EXISTS anonymous
    IDENTIFIED WITH no_password
    HOST ANY
    DEFAULT ROLE frontend_reader
    SETTINGS PROFILE 'anonymous_profile'
    DEFAULT DATABASE default;

CREATE USER IF NOT EXISTS anonymous_heavy
    IDENTIFIED WITH no_password
    HOST ANY
    DEFAULT ROLE frontend_reader
    SETTINGS PROFILE 'anonymous_heavy_profile'
    DEFAULT DATABASE default;

ALTER QUOTA anonymous_quota TO anonymous;
ALTER QUOTA anonymous_heavy_quota TO anonymous_heavy;
