\set ON_ERROR_STOP on

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1
        FROM audit_events
        WHERE request_id = 'smoke-unauthorized-1'
          AND action = 'config.changed'
          AND result = 'DENIED'
          AND parameters->>'error_code' = 'unauthorized'
    ) THEN
        RAISE EXCEPTION 'unauthenticated management request was not audited';
    END IF;

    IF NOT EXISTS (
        SELECT 1
        FROM audit_events
        WHERE request_id = 'smoke-mapping-create'
          AND action = 'config.changed'
          AND result = 'SUCCEEDED'
          AND object_id ~ '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'
          AND parameters->>'source_type' = 'PROMETHEUS'
    ) THEN
        RAISE EXCEPTION 'mapping creation was not audited';
    END IF;

    IF NOT EXISTS (
        SELECT 1
        FROM audit_events
        WHERE request_id = 'smoke-mapping-query'
          AND action = 'tool.called'
          AND result = 'SUCCEEDED'
          AND parameters->>'template_id' = 'prometheus.service.latency.v1'
    ) THEN
        RAISE EXCEPTION 'fixed-template query plan was not audited';
    END IF;

    IF NOT EXISTS (
        SELECT 1
        FROM audit_events
        WHERE request_id = 'smoke-mapping-stale'
          AND action = 'config.changed'
          AND result = 'FAILED'
          AND parameters->>'error_code' = 'stale_version'
    ) THEN
        RAISE EXCEPTION 'stale mapping version rejection was not audited';
    END IF;

    IF EXISTS (
        SELECT 1
        FROM audit_events
        WHERE request_id IN (
            'smoke-unauthorized-1',
            'smoke-mapping-create',
            'smoke-mapping-query',
            'smoke-mapping-stale',
            'smoke-runtime-draft',
            'smoke-runtime-validate'
        )
          AND (
              parameters::text LIKE '%SMOKE_DATASOURCE_TOKEN%'
              OR parameters::text LIKE '%prometheus:9090%'
              OR parameters::text LIKE '%Bearer %'
          )
    ) THEN
        RAISE EXCEPTION 'management audit exposed a secret reference or endpoint';
    END IF;

    IF NOT EXISTS (
        SELECT 1
        FROM data_source_mappings
        WHERE environment = 'prod'
          AND source_type = 'PROMETHEUS'
          AND secret_ref = 'env:SMOKE_DATASOURCE_TOKEN'
          AND deleted_at IS NULL
    ) THEN
        RAISE EXCEPTION 'management API did not persist only the SecretRef locator';
    END IF;
END;
$$;
