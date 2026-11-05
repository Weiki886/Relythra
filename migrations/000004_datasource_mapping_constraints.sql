-- +goose Up
ALTER TABLE data_source_mappings
    ADD COLUMN version integer NOT NULL DEFAULT 1 CHECK (version > 0);

ALTER TABLE data_source_mappings
    ADD CONSTRAINT data_source_mappings_runtime_source_check CHECK (
        (source_type = 'RUNTIME' AND runtime_type IS NOT NULL)
        OR (source_type <> 'RUNTIME' AND runtime_type IS NULL)
    );

ALTER TABLE data_source_mappings
    DROP CONSTRAINT data_source_mappings_service_id_environment_source_type_key;

CREATE UNIQUE INDEX data_source_mappings_active_identity_idx
    ON data_source_mappings (service_id, environment, source_type)
    WHERE deleted_at IS NULL;

COMMENT ON COLUMN data_source_mappings.version IS
    'Optimistic configuration version; incremented on every successful update.';
COMMENT ON INDEX data_source_mappings_active_identity_idx IS
    'One active mapping per service, environment, and source; soft-deleted rows may be recreated.';

-- +goose Down
DROP INDEX IF EXISTS data_source_mappings_active_identity_idx;

ALTER TABLE data_source_mappings
    DROP CONSTRAINT IF EXISTS data_source_mappings_runtime_source_check;

ALTER TABLE data_source_mappings
    DROP COLUMN IF EXISTS version;

ALTER TABLE data_source_mappings
    ADD CONSTRAINT data_source_mappings_service_id_environment_source_type_key
    UNIQUE (service_id, environment, source_type);
