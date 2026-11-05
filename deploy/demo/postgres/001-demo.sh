#!/bin/sh
set -eu
# PostgreSQL runs init scripts once, only for an empty dedicated demo volume.
# psql variable quoting protects credentials; nothing expands into SQL syntax.
reader_password="$(cat /run/demo-secrets/reader_password)"
test -n "$reader_password" || { echo 'Demo reader password is empty' >&2; exit 1; }
psql -v ON_ERROR_STOP=1 --username "$POSTGRES_USER" --dbname demo \
  --set=reader_password="$reader_password" <<'SQL'
CREATE ROLE demo_reader LOGIN PASSWORD :'reader_password' NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT;
REVOKE ALL ON DATABASE demo FROM PUBLIC;
GRANT CONNECT ON DATABASE demo TO demo_reader;
REVOKE CREATE ON SCHEMA public FROM PUBLIC;
GRANT USAGE ON SCHEMA public TO demo_reader;
CREATE TABLE demo_stock (sku text PRIMARY KEY, quantity integer NOT NULL CHECK (quantity >= 0), schema_version integer NOT NULL CHECK (schema_version = 1));
INSERT INTO demo_stock VALUES ('demo-sku', 42, 1);
GRANT SELECT ON demo_stock TO demo_reader;
ALTER ROLE demo_reader SET default_transaction_read_only = on;
ALTER ROLE demo_reader SET statement_timeout = '1500ms';
ALTER ROLE demo_reader CONNECTION LIMIT 8;
SQL
unset reader_password
