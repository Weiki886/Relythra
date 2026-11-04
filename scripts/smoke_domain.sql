\set ON_ERROR_STOP on

BEGIN;

DO $$
DECLARE
    missing_relations text;
BEGIN
    SELECT string_agg(relation_name, ', ' ORDER BY relation_name)
    INTO missing_relations
    FROM unnest(ARRAY[
        'projects',
        'services',
        'releases',
        'deployment_events',
        'data_source_mappings',
        'alerts',
        'incidents',
        'investigations',
        'tool_calls',
        'evidence',
        'hypotheses',
        'diagnosis_cases',
        'reports',
        'notifications',
        'deployment_candidates',
        'hypothesis_evidence',
        'investigation_transition_audits'
    ]) AS expected(relation_name)
    WHERE to_regclass(relation_name) IS NULL;

    IF missing_relations IS NOT NULL THEN
        RAISE EXCEPTION 'domain relations are missing: %', missing_relations;
    END IF;
END;
$$;

DO $$
DECLARE
    actual_indexes integer;
BEGIN
    SELECT count(*)
    INTO actual_indexes
    FROM pg_indexes
    WHERE schemaname = current_schema()
      AND indexname IN (
          'deployment_events_service_environment_started_idx',
          'deployment_events_project_started_idx',
          'alerts_service_started_idx',
          'investigations_status_updated_idx',
          'evidence_investigation_observed_idx',
          'reports_generated_idx'
      );

    IF actual_indexes <> 6 THEN
        RAISE EXCEPTION 'expected 6 domain indexes, found %', actual_indexes;
    END IF;
END;
$$;

INSERT INTO projects (id, slug, name)
VALUES (
    '10000000-0000-4000-8000-000000000001',
    'smoke-checkout',
    'Smoke Checkout'
);

INSERT INTO services (id, project_id, slug, name)
VALUES (
    '10000000-0000-4000-8000-000000000002',
    '10000000-0000-4000-8000-000000000001',
    'checkout-api',
    'Checkout API'
);

INSERT INTO releases (
    id, project_id, service_id, version, commit_sha, image_digest
) VALUES (
    '10000000-0000-4000-8000-000000000003',
    '10000000-0000-4000-8000-000000000001',
    '10000000-0000-4000-8000-000000000002',
    'v1.2.3',
    repeat('a', 40),
    'sha256:' || repeat('b', 64)
);

INSERT INTO deployment_events (
    id, project_id, service_id, release_id, environment, source_system,
    idempotency_key, status, started_at, finished_at, version, commit_sha,
    image_digest, instance_count, source_reference, source_timezone,
    source_time_text, metadata
) VALUES (
    '10000000-0000-4000-8000-000000000004',
    '10000000-0000-4000-8000-000000000001',
    '10000000-0000-4000-8000-000000000002',
    '10000000-0000-4000-8000-000000000003',
    'prod',
    'ci',
    'smoke-deploy-1',
    'SUCCEEDED',
    '2026-11-04T05:00:00Z',
    '2026-11-04T05:02:00Z',
    'v1.2.3',
    repeat('a', 40),
    'sha256:' || repeat('b', 64),
    2,
    'https://example.test/deployments/smoke-1',
    'Asia/Shanghai',
    '2026-11-04T13:00:00+08:00',
    '{"pipeline":"smoke"}'::jsonb
)
ON CONFLICT DO NOTHING;

INSERT INTO deployment_events (
    project_id, service_id, release_id, environment, source_system,
    idempotency_key, status, started_at, version, commit_sha
) VALUES (
    '10000000-0000-4000-8000-000000000001',
    '10000000-0000-4000-8000-000000000002',
    '10000000-0000-4000-8000-000000000003',
    'prod',
    'ci',
    'smoke-deploy-1',
    'FAILED',
    '2026-11-04T05:00:00Z',
    'v1.2.3',
    repeat('a', 40)
)
ON CONFLICT DO NOTHING;

DO $$
BEGIN
    IF (
        SELECT count(*)
        FROM deployment_events
        WHERE idempotency_key = 'smoke-deploy-1'
    ) <> 1 THEN
        RAISE EXCEPTION 'deployment idempotency key did not remain unique';
    END IF;
END;
$$;

INSERT INTO alerts (
    id, project_id, service_id, environment, fingerprint, idempotency_key,
    status, severity, summary, starts_at, labels, annotations, redacted_payload
) VALUES (
    '10000000-0000-4000-8000-000000000005',
    '10000000-0000-4000-8000-000000000001',
    '10000000-0000-4000-8000-000000000002',
    'prod',
    'checkout-latency',
    'smoke-alert-1',
    'FIRING',
    'ERROR',
    'Checkout latency is elevated',
    '2026-11-04T05:00:00Z',
    '{"service":"checkout-api"}'::jsonb,
    '{"dashboard":"checkout"}'::jsonb,
    '{"receiver":"relythra","status":"firing"}'::jsonb
)
ON CONFLICT DO NOTHING;

INSERT INTO alerts (
    project_id, service_id, environment, fingerprint, idempotency_key,
    status, severity, summary, starts_at
) VALUES (
    '10000000-0000-4000-8000-000000000001',
    '10000000-0000-4000-8000-000000000002',
    'prod',
    'checkout-latency',
    'smoke-alert-2',
    'FIRING',
    'ERROR',
    'Checkout latency is elevated',
    '2026-11-04T05:00:00Z'
)
ON CONFLICT DO NOTHING;

DO $$
BEGIN
    IF (
        SELECT count(*)
        FROM alerts
        WHERE project_id = '10000000-0000-4000-8000-000000000001'
          AND fingerprint = 'checkout-latency'
          AND starts_at = '2026-11-04T05:00:00Z'
    ) <> 1 THEN
        RAISE EXCEPTION 'alert fingerprint window did not remain unique';
    END IF;
END;
$$;

INSERT INTO incidents (
    id, project_id, service_id, status, severity, summary, environment, started_at
) VALUES (
    '10000000-0000-4000-8000-000000000006',
    '10000000-0000-4000-8000-000000000001',
    '10000000-0000-4000-8000-000000000002',
    'OPEN',
    'ERROR',
    'Checkout latency incident',
    'prod',
    '2026-11-04T05:00:00Z'
);

INSERT INTO investigations (
    id, incident_id, service_id, environment
) VALUES (
    '10000000-0000-4000-8000-000000000007',
    '10000000-0000-4000-8000-000000000006',
    '10000000-0000-4000-8000-000000000002',
    'prod'
);

DO $$
BEGIN
    BEGIN
        UPDATE investigations
        SET status = 'COMPLETED', completed_at = now(), version = 2
        WHERE id = '10000000-0000-4000-8000-000000000007';

        RAISE EXCEPTION 'invalid investigation transition was accepted';
    EXCEPTION
        WHEN check_violation THEN NULL;
    END;
END;
$$;

DO $$
BEGIN
    IF (
        SELECT status || ':' || version
        FROM investigations
        WHERE id = '10000000-0000-4000-8000-000000000007'
    ) <> 'RECEIVED:1' THEN
        RAISE EXCEPTION 'invalid investigation transition was not rolled back';
    END IF;
END;
$$;

UPDATE investigations
SET status = 'MATCHED', version = 2
WHERE id = '10000000-0000-4000-8000-000000000007';

DO $$
BEGIN
    IF (
        SELECT count(*)
        FROM investigation_transition_audits
        WHERE investigation_id = '10000000-0000-4000-8000-000000000007'
    ) <> 2 THEN
        RAISE EXCEPTION 'investigation transition audit is incomplete';
    END IF;
END;
$$;

INSERT INTO tool_calls (
    id, investigation_id, tool_name, status, completed_at, result_summary
) VALUES (
    '10000000-0000-4000-8000-000000000008',
    '10000000-0000-4000-8000-000000000007',
    'query_metric_template',
    'SUCCEEDED',
    '2026-11-04T05:05:00Z',
    '{"series":3}'::jsonb
);

INSERT INTO evidence (
    id, investigation_id, tool_call_id, source_type, entity_type, entity_id,
    observed_start, observed_end, query_text, raw_reference, value_summary,
    completeness, freshness_seconds, independence_group, content_hash,
    source_timezone, source_time_text
) VALUES (
    '10000000-0000-4000-8000-000000000009',
    '10000000-0000-4000-8000-000000000007',
    '10000000-0000-4000-8000-000000000008',
    'PROMETHEUS',
    'service',
    '10000000-0000-4000-8000-000000000002',
    '2026-11-04T05:00:00Z',
    '2026-11-04T05:05:00Z',
    'latency_p95{service="checkout-api"}',
    'https://example.test/prometheus/graph/smoke',
    '{"p95_ms":420}'::jsonb,
    'COMPLETE',
    15,
    'metrics',
    repeat('c', 64),
    'Asia/Shanghai',
    '2026-11-04T13:00:00+08:00'
);

INSERT INTO hypotheses (
    id, investigation_id, rank, entity, fault_type, mechanism, trigger_change,
    affected_scope, confidence, summary
) VALUES (
    '10000000-0000-4000-8000-000000000010',
    '10000000-0000-4000-8000-000000000007',
    1,
    'checkout-api',
    'slow-sql',
    'inefficient query',
    'release v1.2.3',
    'prod',
    'HIGH',
    'Release introduced an inefficient query'
);

INSERT INTO hypothesis_evidence (
    investigation_id, hypothesis_id, evidence_id, stance
) VALUES (
    '10000000-0000-4000-8000-000000000007',
    '10000000-0000-4000-8000-000000000010',
    '10000000-0000-4000-8000-000000000009',
    'SUPPORTS'
);

INSERT INTO deployment_candidates (
    project_id, incident_id, deployment_event_id, match_status, match_reason
) VALUES (
    '10000000-0000-4000-8000-000000000001',
    '10000000-0000-4000-8000-000000000006',
    '10000000-0000-4000-8000-000000000004',
    'MATCHED',
    'same service and environment within release window'
);

INSERT INTO reports (
    id, investigation_id, result_status, confidence, summary, recommendations,
    data_gaps, timeline
) VALUES (
    '10000000-0000-4000-8000-000000000011',
    '10000000-0000-4000-8000-000000000007',
    'COMPLETED',
    'HIGH',
    'Deployment introduced a slow query',
    '["inspect query plan"]'::jsonb,
    '[]'::jsonb,
    '[{"at":"2026-11-04T05:00:00Z","kind":"deployment"}]'::jsonb
);

DO $$
BEGIN
    BEGIN
        UPDATE reports
        SET summary = 'mutated report'
        WHERE id = '10000000-0000-4000-8000-000000000011';

        RAISE EXCEPTION 'report mutation was accepted';
    EXCEPTION
        WHEN SQLSTATE '23001' THEN NULL;
    END;
END;
$$;

DO $$
BEGIN
    IF (
        SELECT count(*)
        FROM evidence
        WHERE investigation_id = '10000000-0000-4000-8000-000000000007'
    ) <> 1 THEN
        RAISE EXCEPTION 'report evidence association is missing';
    END IF;

    IF (
        SELECT count(*)
        FROM deployment_candidates
        WHERE project_id = '10000000-0000-4000-8000-000000000001'
          AND incident_id = '10000000-0000-4000-8000-000000000006'
          AND deployment_event_id = '10000000-0000-4000-8000-000000000004'
          AND match_status = 'MATCHED'
    ) <> 1 THEN
        RAISE EXCEPTION 'report deployment association is missing';
    END IF;
END;
$$;

COMMIT;
