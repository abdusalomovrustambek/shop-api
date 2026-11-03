-- Roles and schemas of the Shop database. Run as the postgres superuser, in the shop database:
--
--   docker compose -p shop exec -T postgres psql -U postgres -d shop < roles.sql
--
-- The roles are created without a login secret; the operator sets one for each role from
-- the values in .env (see AGENTS.md, "Deploy"). The postgres superuser is used only for
-- this and is in no service's environment.
--
-- The script is idempotent. Run it before the first start, and once more after billing-worker
-- has created billing.jobs: the grants on that table can only be made once it exists.
--
-- No role here is a superuser or owns the database.

SELECT format('CREATE ROLE %I LOGIN', r) FROM (VALUES ('shop_api'), ('billing'), ('auth')) AS t(r)
WHERE NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = t.r) \gexec

REVOKE ALL ON DATABASE shop FROM PUBLIC;
GRANT CONNECT ON DATABASE shop TO shop_api, billing, auth;
REVOKE ALL ON SCHEMA public FROM PUBLIC;

-- Each service owns its schema, so its migrations can create tables there.
-- shop-api needs DDL in schema shop: goose creates shop.goose_db_version and the tables.
CREATE SCHEMA IF NOT EXISTS shop AUTHORIZATION shop_api;
CREATE SCHEMA IF NOT EXISTS billing AUTHORIZATION billing;
CREATE SCHEMA IF NOT EXISTS auth AUTHORIZATION auth;

-- billing-worker reads and updates shop.orders; the table grants are made by shop-api's
-- migrations, which run as the table owner.
GRANT USAGE ON SCHEMA shop TO billing;

-- shop-api queues charge_order jobs.
GRANT USAGE ON SCHEMA billing TO shop_api;

-- Skipped until billing-worker's migration has created billing.jobs.
SELECT format('GRANT INSERT ON billing.jobs TO shop_api; GRANT USAGE ON SEQUENCE %s TO shop_api',
              pg_get_serial_sequence('billing.jobs', 'id'))
WHERE to_regclass('billing.jobs') IS NOT NULL \gexec
