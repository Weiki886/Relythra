-- +goose Up
CREATE TABLE projects (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    slug text NOT NULL CHECK (slug ~ '^[a-z0-9][a-z0-9-]{0,62}$'),
    name text NOT NULL CHECK (btrim(name) <> ''),
    description text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    deleted_at timestamptz,
    UNIQUE (slug)
);

CREATE TABLE services (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    project_id uuid NOT NULL REFERENCES projects(id) ON DELETE RESTRICT,
    slug text NOT NULL CHECK (slug ~ '^[a-z0-9][a-z0-9-]{0,62}$'),
    name text NOT NULL CHECK (btrim(name) <> ''),
    description text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    deleted_at timestamptz,
    UNIQUE (project_id, slug),
    UNIQUE (id, project_id)
);

CREATE TABLE releases (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    project_id uuid NOT NULL REFERENCES projects(id) ON DELETE RESTRICT,
    service_id uuid NOT NULL,
    version text NOT NULL CHECK (btrim(version) <> ''),
    commit_sha text NOT NULL CHECK (commit_sha ~ '^(?:[0-9a-f]{40}|[0-9a-f]{64})$'),
    image_digest text CHECK (image_digest IS NULL OR image_digest ~ '^sha256:[0-9a-f]{64}$'),
    pull_requests jsonb NOT NULL DEFAULT '[]'::jsonb CHECK (jsonb_typeof(pull_requests) = 'array'),
    source_reference text NOT NULL DEFAULT '',
    source_timezone text,
    source_time_text text,
    metadata jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(metadata) = 'object'),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    deleted_at timestamptz,
    FOREIGN KEY (service_id, project_id) REFERENCES services(id, project_id) ON DELETE RESTRICT,
    UNIQUE (service_id, version),
    UNIQUE (id, service_id, project_id)
);

CREATE TABLE deployment_events (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    project_id uuid NOT NULL REFERENCES projects(id) ON DELETE RESTRICT,
    service_id uuid NOT NULL,
    release_id uuid NOT NULL,
    environment text NOT NULL CHECK (environment ~ '^[a-z0-9][a-z0-9-]{0,31}$'),
    source_system text NOT NULL CHECK (btrim(source_system) <> ''),
    idempotency_key text NOT NULL CHECK (btrim(idempotency_key) <> ''),
    status text NOT NULL CHECK (status IN ('PENDING', 'IN_PROGRESS', 'SUCCEEDED', 'FAILED', 'ROLLED_BACK', 'UNKNOWN')),
    started_at timestamptz NOT NULL,
    finished_at timestamptz,
    version text NOT NULL CHECK (btrim(version) <> ''),
    commit_sha text NOT NULL CHECK (commit_sha ~ '^(?:[0-9a-f]{40}|[0-9a-f]{64})$'),
    image_digest text CHECK (image_digest IS NULL OR image_digest ~ '^sha256:[0-9a-f]{64}$'),
    instance_count integer NOT NULL DEFAULT 1 CHECK (instance_count > 0),
    source_reference text NOT NULL DEFAULT '',
    source_timezone text,
    source_time_text text,
    metadata jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(metadata) = 'object'),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    FOREIGN KEY (service_id, project_id) REFERENCES services(id, project_id) ON DELETE RESTRICT,
    FOREIGN KEY (release_id, service_id, project_id) REFERENCES releases(id, service_id, project_id) ON DELETE RESTRICT,
    UNIQUE (service_id, environment, source_system, idempotency_key),
    UNIQUE (id, project_id),
    CHECK (finished_at IS NULL OR finished_at >= started_at)
);

CREATE TABLE data_source_mappings (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    project_id uuid NOT NULL REFERENCES projects(id) ON DELETE RESTRICT,
    service_id uuid NOT NULL,
    environment text NOT NULL CHECK (environment ~ '^[a-z0-9][a-z0-9-]{0,31}$'),
    source_type text NOT NULL CHECK (source_type IN ('GITHUB', 'GRAFANA', 'PROMETHEUS', 'LOKI', 'RUNTIME')),
    runtime_type text CHECK (runtime_type IS NULL OR runtime_type IN ('docker-compose', 'kubernetes')),
    public_settings jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(public_settings) = 'object'),
    query_templates jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(query_templates) = 'object'),
    secret_ref text CHECK (secret_ref IS NULL OR btrim(secret_ref) <> ''),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    deleted_at timestamptz,
    FOREIGN KEY (service_id, project_id) REFERENCES services(id, project_id) ON DELETE RESTRICT,
    UNIQUE (service_id, environment, source_type),
    CHECK (source_type <> 'RUNTIME' OR runtime_type IS NOT NULL)
);

CREATE TABLE alerts (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    project_id uuid NOT NULL REFERENCES projects(id) ON DELETE RESTRICT,
    service_id uuid,
    environment text,
    fingerprint text NOT NULL CHECK (btrim(fingerprint) <> ''),
    idempotency_key text NOT NULL CHECK (btrim(idempotency_key) <> ''),
    status text NOT NULL CHECK (status IN ('FIRING', 'RESOLVED', 'UNKNOWN')),
    severity text NOT NULL CHECK (severity IN ('INFO', 'WARNING', 'ERROR', 'CRITICAL', 'UNKNOWN')),
    summary text NOT NULL CHECK (btrim(summary) <> ''),
    starts_at timestamptz NOT NULL,
    ends_at timestamptz,
    labels jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(labels) = 'object'),
    annotations jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(annotations) = 'object'),
    redacted_payload jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(redacted_payload) = 'object'),
    source_reference text NOT NULL DEFAULT '',
    source_timezone text,
    source_time_text text,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    FOREIGN KEY (service_id, project_id) REFERENCES services(id, project_id) ON DELETE RESTRICT,
    UNIQUE (project_id, fingerprint, starts_at),
    UNIQUE (project_id, idempotency_key),
    CHECK (ends_at IS NULL OR ends_at >= starts_at),
    CHECK (service_id IS NOT NULL OR environment IS NULL)
);

CREATE TABLE incidents (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    project_id uuid NOT NULL REFERENCES projects(id) ON DELETE RESTRICT,
    service_id uuid,
    status text NOT NULL CHECK (status IN ('OPEN', 'INVESTIGATING', 'RESOLVED', 'CLOSED')),
    severity text NOT NULL CHECK (severity IN ('INFO', 'WARNING', 'ERROR', 'CRITICAL', 'UNKNOWN')),
    summary text NOT NULL CHECK (btrim(summary) <> ''),
    environment text,
    started_at timestamptz NOT NULL,
    ended_at timestamptz,
    labels jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(labels) = 'object'),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    FOREIGN KEY (service_id, project_id) REFERENCES services(id, project_id) ON DELETE RESTRICT,
    UNIQUE (id, project_id),
    CHECK (ended_at IS NULL OR ended_at >= started_at),
    CHECK (service_id IS NOT NULL OR environment IS NULL)
);

CREATE TABLE investigations (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    incident_id uuid NOT NULL REFERENCES incidents(id) ON DELETE RESTRICT,
    service_id uuid,
    environment text,
    attempt integer NOT NULL DEFAULT 1 CHECK (attempt > 0),
    version bigint NOT NULL DEFAULT 1 CHECK (version > 0),
    status text NOT NULL DEFAULT 'RECEIVED' CHECK (status IN (
        'RECEIVED',
        'MATCHED',
        'COLLECTING',
        'HYPOTHESIZING',
        'VERIFYING',
        'COMPLETED',
        'INSUFFICIENT_EVIDENCE',
        'FAILED'
    )),
    current_step text,
    model text,
    prompt_version text,
    error_code text,
    error_message text,
    started_at timestamptz NOT NULL DEFAULT now(),
    completed_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (incident_id, attempt),
    CHECK (
        (status IN ('COMPLETED', 'INSUFFICIENT_EVIDENCE', 'FAILED') AND completed_at IS NOT NULL)
        OR
        (status NOT IN ('COMPLETED', 'INSUFFICIENT_EVIDENCE', 'FAILED') AND completed_at IS NULL)
    )
);

CREATE TABLE tool_calls (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    investigation_id uuid NOT NULL REFERENCES investigations(id) ON DELETE RESTRICT,
    tool_name text NOT NULL CHECK (btrim(tool_name) <> ''),
    tool_version text NOT NULL DEFAULT 'v1',
    status text NOT NULL DEFAULT 'STARTED' CHECK (status IN ('STARTED', 'SUCCEEDED', 'FAILED')),
    arguments jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(arguments) = 'object'),
    query_text text,
    result_summary jsonb CHECK (result_summary IS NULL OR jsonb_typeof(result_summary) = 'object'),
    raw_reference text,
    error_code text,
    error_message text,
    started_at timestamptz NOT NULL DEFAULT now(),
    completed_at timestamptz,
    duration_ms integer CHECK (duration_ms IS NULL OR duration_ms >= 0),
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (id, investigation_id),
    CHECK (
        (status = 'STARTED' AND completed_at IS NULL)
        OR
        (status IN ('SUCCEEDED', 'FAILED') AND completed_at IS NOT NULL)
    )
);

CREATE TABLE evidence (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    investigation_id uuid NOT NULL REFERENCES investigations(id) ON DELETE RESTRICT,
    tool_call_id uuid,
    source_type text NOT NULL CHECK (source_type IN (
        'DEPLOYMENT',
        'GITHUB',
        'PROMETHEUS',
        'LOKI',
        'RUNTIME',
        'MAPPING',
        'SYSTEM'
    )),
    entity_type text NOT NULL CHECK (btrim(entity_type) <> ''),
    entity_id text,
    observed_start timestamptz NOT NULL,
    observed_end timestamptz NOT NULL,
    query_text text,
    raw_reference text,
    value_summary jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(value_summary) = 'object'),
    completeness text NOT NULL CHECK (completeness IN ('COMPLETE', 'PARTIAL', 'MISSING')),
    freshness_seconds integer CHECK (freshness_seconds IS NULL OR freshness_seconds >= 0),
    independence_group text NOT NULL CHECK (btrim(independence_group) <> ''),
    content_hash text NOT NULL CHECK (content_hash ~ '^[0-9a-f]{64}$'),
    source_timezone text,
    source_time_text text,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (id, investigation_id),
    UNIQUE (investigation_id, source_type, content_hash),
    FOREIGN KEY (tool_call_id, investigation_id)
        REFERENCES tool_calls(id, investigation_id) ON DELETE RESTRICT,
    CHECK (observed_end >= observed_start)
);

CREATE TABLE hypotheses (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    investigation_id uuid NOT NULL REFERENCES investigations(id) ON DELETE RESTRICT,
    rank smallint NOT NULL CHECK (rank BETWEEN 1 AND 3),
    entity text NOT NULL CHECK (btrim(entity) <> ''),
    fault_type text NOT NULL CHECK (btrim(fault_type) <> ''),
    mechanism text NOT NULL CHECK (btrim(mechanism) <> ''),
    trigger_change text,
    affected_scope text NOT NULL CHECK (btrim(affected_scope) <> ''),
    confidence text NOT NULL CHECK (confidence IN ('HIGH', 'MEDIUM', 'LOW')),
    summary text NOT NULL CHECK (btrim(summary) <> ''),
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (id, investigation_id),
    UNIQUE (investigation_id, rank)
);

CREATE TABLE hypothesis_evidence (
    investigation_id uuid NOT NULL REFERENCES investigations(id) ON DELETE RESTRICT,
    hypothesis_id uuid NOT NULL,
    evidence_id uuid NOT NULL,
    stance text NOT NULL CHECK (stance IN ('SUPPORTS', 'CONTRADICTS')),
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (hypothesis_id, evidence_id, stance),
    FOREIGN KEY (hypothesis_id, investigation_id)
        REFERENCES hypotheses(id, investigation_id) ON DELETE RESTRICT,
    FOREIGN KEY (evidence_id, investigation_id)
        REFERENCES evidence(id, investigation_id) ON DELETE RESTRICT
);

CREATE TABLE diagnosis_cases (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    case_key text NOT NULL CHECK (btrim(case_key) <> ''),
    case_version integer NOT NULL CHECK (case_version > 0),
    project_id uuid REFERENCES projects(id) ON DELETE RESTRICT,
    fault_type text NOT NULL CHECK (btrim(fault_type) <> ''),
    title text NOT NULL CHECK (btrim(title) <> ''),
    injection_method jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(injection_method) = 'object'),
    expected_root_cause jsonb NOT NULL CHECK (jsonb_typeof(expected_root_cause) = 'object'),
    allowed_variants jsonb NOT NULL DEFAULT '[]'::jsonb CHECK (jsonb_typeof(allowed_variants) = 'array'),
    required_evidence jsonb NOT NULL DEFAULT '[]'::jsonb CHECK (jsonb_typeof(required_evidence) = 'array'),
    counterexample_conditions jsonb NOT NULL DEFAULT '[]'::jsonb CHECK (jsonb_typeof(counterexample_conditions) = 'array'),
    healthy_baseline jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(healthy_baseline) = 'object'),
    scenario_metadata jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(scenario_metadata) = 'object'),
    active boolean NOT NULL DEFAULT true,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    deleted_at timestamptz,
    UNIQUE (case_key, case_version)
);

CREATE TABLE reports (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    investigation_id uuid NOT NULL REFERENCES investigations(id) ON DELETE RESTRICT,
    diagnosis_case_id uuid REFERENCES diagnosis_cases(id) ON DELETE RESTRICT,
    result_status text NOT NULL CHECK (result_status IN ('COMPLETED', 'INSUFFICIENT_EVIDENCE', 'FAILED')),
    confidence text NOT NULL CHECK (confidence IN ('HIGH', 'MEDIUM', 'LOW', 'INCONCLUSIVE')),
    summary text NOT NULL CHECK (btrim(summary) <> ''),
    recommendations jsonb NOT NULL DEFAULT '[]'::jsonb CHECK (jsonb_typeof(recommendations) = 'array'),
    data_gaps jsonb NOT NULL DEFAULT '[]'::jsonb CHECK (jsonb_typeof(data_gaps) = 'array'),
    timeline jsonb NOT NULL DEFAULT '[]'::jsonb CHECK (jsonb_typeof(timeline) = 'array'),
    evaluation_key text,
    generated_at timestamptz NOT NULL DEFAULT now(),
    model text,
    prompt_version text,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (investigation_id)
);

CREATE TABLE notifications (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    report_id uuid NOT NULL REFERENCES reports(id) ON DELETE RESTRICT,
    channel text NOT NULL CHECK (channel IN ('FEISHU', 'WECHAT_WORK')),
    target_ref text NOT NULL CHECK (btrim(target_ref) <> ''),
    message_version integer NOT NULL DEFAULT 1 CHECK (message_version > 0),
    attempt integer NOT NULL DEFAULT 1 CHECK (attempt > 0),
    status text NOT NULL CHECK (status IN ('PENDING', 'SENDING', 'SENT', 'FAILED')),
    idempotency_key text NOT NULL CHECK (btrim(idempotency_key) <> ''),
    summary text NOT NULL CHECK (btrim(summary) <> ''),
    error_code text,
    error_message text,
    sent_at timestamptz,
    retry_after timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (report_id, channel, message_version, attempt),
    UNIQUE (channel, idempotency_key),
    CHECK ((status = 'SENT' AND sent_at IS NOT NULL) OR status <> 'SENT')
);

CREATE TABLE deployment_candidates (
    project_id uuid NOT NULL REFERENCES projects(id) ON DELETE RESTRICT,
    incident_id uuid NOT NULL,
    deployment_event_id uuid NOT NULL,
    match_status text NOT NULL CHECK (match_status IN ('PENDING', 'MATCHED', 'AMBIGUOUS', 'MISSING')),
    match_reason text NOT NULL CHECK (btrim(match_reason) <> ''),
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (incident_id, deployment_event_id),
    FOREIGN KEY (incident_id, project_id)
        REFERENCES incidents(id, project_id) ON DELETE RESTRICT,
    FOREIGN KEY (deployment_event_id, project_id)
        REFERENCES deployment_events(id, project_id) ON DELETE RESTRICT
);

CREATE TABLE investigation_transition_audits (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    investigation_id uuid NOT NULL REFERENCES investigations(id) ON DELETE RESTRICT,
    from_status text,
    to_status text NOT NULL,
    version bigint NOT NULL CHECK (version > 0),
    occurred_at timestamptz NOT NULL DEFAULT now(),
    CHECK (from_status IS NULL OR from_status IN (
        'RECEIVED',
        'MATCHED',
        'COLLECTING',
        'HYPOTHESIZING',
        'VERIFYING',
        'COMPLETED',
        'INSUFFICIENT_EVIDENCE',
        'FAILED'
    )),
    CHECK (to_status IN (
        'RECEIVED',
        'MATCHED',
        'COLLECTING',
        'HYPOTHESIZING',
        'VERIFYING',
        'COMPLETED',
        'INSUFFICIENT_EVIDENCE',
        'FAILED'
    ))
);

CREATE INDEX services_project_active_idx
    ON services (project_id, created_at)
    WHERE deleted_at IS NULL;
CREATE INDEX releases_service_created_idx
    ON releases (service_id, created_at DESC)
    WHERE deleted_at IS NULL;
CREATE INDEX deployment_events_service_environment_started_idx
    ON deployment_events (service_id, environment, started_at DESC);
CREATE INDEX deployment_events_project_started_idx
    ON deployment_events (project_id, started_at DESC);
CREATE INDEX data_source_mappings_project_active_idx
    ON data_source_mappings (project_id, environment, source_type)
    WHERE deleted_at IS NULL;
CREATE INDEX alerts_service_started_idx
    ON alerts (service_id, starts_at DESC)
    WHERE service_id IS NOT NULL;
CREATE INDEX alerts_project_started_idx
    ON alerts (project_id, starts_at DESC);
CREATE INDEX incidents_project_started_idx
    ON incidents (project_id, started_at DESC);
CREATE INDEX incidents_service_started_idx
    ON incidents (service_id, started_at DESC)
    WHERE service_id IS NOT NULL;
CREATE INDEX investigations_incident_idx
    ON investigations (incident_id, attempt DESC);
CREATE INDEX investigations_service_started_idx
    ON investigations (service_id, started_at DESC)
    WHERE service_id IS NOT NULL;
CREATE INDEX investigations_status_updated_idx
    ON investigations (status, updated_at);
CREATE INDEX tool_calls_investigation_created_idx
    ON tool_calls (investigation_id, created_at);
CREATE INDEX evidence_investigation_observed_idx
    ON evidence (investigation_id, observed_start, observed_end);
CREATE INDEX evidence_tool_call_idx
    ON evidence (tool_call_id)
    WHERE tool_call_id IS NOT NULL;
CREATE INDEX hypothesis_evidence_evidence_idx
    ON hypothesis_evidence (evidence_id, investigation_id);
CREATE INDEX diagnosis_cases_fault_active_idx
    ON diagnosis_cases (fault_type, active)
    WHERE deleted_at IS NULL;
CREATE INDEX reports_generated_idx
    ON reports (generated_at DESC);
CREATE INDEX notifications_retry_idx
    ON notifications (status, retry_after)
    WHERE status IN ('PENDING', 'FAILED');
CREATE INDEX investigation_transition_audits_investigation_idx
    ON investigation_transition_audits (investigation_id, occurred_at);

-- +goose StatementBegin
CREATE FUNCTION touch_updated_at()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    NEW.updated_at := now();
    RETURN NEW;
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION reject_mutation()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    RAISE EXCEPTION 'immutable record cannot be modified or deleted'
        USING ERRCODE = '23001';
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION reject_terminal_tool_call_mutation()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF OLD.status IN ('SUCCEEDED', 'FAILED') THEN
        RAISE EXCEPTION 'terminal tool call cannot be modified or deleted'
            USING ERRCODE = '23001';
    END IF;

    IF TG_OP = 'UPDATE' AND NEW.status <> OLD.status
       AND NOT (OLD.status = 'STARTED' AND NEW.status IN ('SUCCEEDED', 'FAILED')) THEN
        RAISE EXCEPTION 'invalid tool call transition from % to %', OLD.status, NEW.status
            USING ERRCODE = '23514';
    END IF;

    RETURN NEW;
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION enforce_investigation_transition()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
    terminal_statuses constant text[] := ARRAY['COMPLETED', 'INSUFFICIENT_EVIDENCE', 'FAILED'];
BEGIN
    IF TG_OP = 'INSERT' THEN
        IF NEW.status <> 'RECEIVED' THEN
            RAISE EXCEPTION 'investigation must start in RECEIVED'
                USING ERRCODE = '23514';
        END IF;
        RETURN NEW;
    END IF;

    IF OLD.status = ANY(terminal_statuses) THEN
        RAISE EXCEPTION 'terminal investigation cannot be modified or deleted'
            USING ERRCODE = '23001';
    END IF;

    IF NEW.status = OLD.status THEN
        RETURN NEW;
    END IF;

    IF NEW.version <> OLD.version + 1 THEN
        RAISE EXCEPTION 'investigation version must advance by one on transition'
            USING ERRCODE = '40001';
    END IF;

    IF NOT (
        (OLD.status = 'RECEIVED' AND NEW.status = 'MATCHED')
        OR (OLD.status = 'MATCHED' AND NEW.status = 'COLLECTING')
        OR (OLD.status = 'COLLECTING' AND NEW.status = 'HYPOTHESIZING')
        OR (OLD.status = 'HYPOTHESIZING' AND NEW.status = 'VERIFYING')
        OR (OLD.status = 'VERIFYING' AND NEW.status IN ('COMPLETED', 'INSUFFICIENT_EVIDENCE', 'FAILED'))
    ) THEN
        RAISE EXCEPTION 'invalid investigation transition from % to %', OLD.status, NEW.status
            USING ERRCODE = '23514';
    END IF;

    RETURN NEW;
END;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION audit_investigation_transition()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    INSERT INTO investigation_transition_audits (
        investigation_id,
        from_status,
        to_status,
        version
    ) VALUES (
        NEW.id,
        CASE WHEN TG_OP = 'INSERT' THEN NULL ELSE OLD.status END,
        NEW.status,
        NEW.version
    );
    RETURN NEW;
END;
$$;
-- +goose StatementEnd

CREATE TRIGGER projects_touch_updated_at
BEFORE UPDATE ON projects
FOR EACH ROW EXECUTE FUNCTION touch_updated_at();
CREATE TRIGGER services_touch_updated_at
BEFORE UPDATE ON services
FOR EACH ROW EXECUTE FUNCTION touch_updated_at();
CREATE TRIGGER releases_touch_updated_at
BEFORE UPDATE ON releases
FOR EACH ROW EXECUTE FUNCTION touch_updated_at();
CREATE TRIGGER deployment_events_touch_updated_at
BEFORE UPDATE ON deployment_events
FOR EACH ROW EXECUTE FUNCTION touch_updated_at();
CREATE TRIGGER data_source_mappings_touch_updated_at
BEFORE UPDATE ON data_source_mappings
FOR EACH ROW EXECUTE FUNCTION touch_updated_at();
CREATE TRIGGER alerts_touch_updated_at
BEFORE UPDATE ON alerts
FOR EACH ROW EXECUTE FUNCTION touch_updated_at();
CREATE TRIGGER incidents_touch_updated_at
BEFORE UPDATE ON incidents
FOR EACH ROW EXECUTE FUNCTION touch_updated_at();
CREATE TRIGGER diagnosis_cases_touch_updated_at
BEFORE UPDATE ON diagnosis_cases
FOR EACH ROW EXECUTE FUNCTION touch_updated_at();
CREATE TRIGGER notifications_touch_updated_at
BEFORE UPDATE ON notifications
FOR EACH ROW EXECUTE FUNCTION touch_updated_at();

CREATE TRIGGER investigations_transition_guard
BEFORE INSERT OR UPDATE ON investigations
FOR EACH ROW EXECUTE FUNCTION enforce_investigation_transition();
CREATE TRIGGER investigations_touch_updated_at
BEFORE UPDATE ON investigations
FOR EACH ROW EXECUTE FUNCTION touch_updated_at();
CREATE TRIGGER investigations_audit_transition
AFTER INSERT OR UPDATE ON investigations
FOR EACH ROW EXECUTE FUNCTION audit_investigation_transition();

CREATE TRIGGER tool_calls_reject_mutation
BEFORE UPDATE OR DELETE ON tool_calls
FOR EACH ROW EXECUTE FUNCTION reject_terminal_tool_call_mutation();
CREATE TRIGGER evidence_reject_mutation
BEFORE UPDATE OR DELETE ON evidence
FOR EACH ROW EXECUTE FUNCTION reject_mutation();
CREATE TRIGGER hypotheses_reject_mutation
BEFORE UPDATE OR DELETE ON hypotheses
FOR EACH ROW EXECUTE FUNCTION reject_mutation();
CREATE TRIGGER hypothesis_evidence_reject_mutation
BEFORE UPDATE OR DELETE ON hypothesis_evidence
FOR EACH ROW EXECUTE FUNCTION reject_mutation();
CREATE TRIGGER reports_reject_mutation
BEFORE UPDATE OR DELETE ON reports
FOR EACH ROW EXECUTE FUNCTION reject_mutation();
CREATE TRIGGER investigation_transition_audits_reject_mutation
BEFORE UPDATE OR DELETE ON investigation_transition_audits
FOR EACH ROW EXECUTE FUNCTION reject_mutation();

COMMENT ON TABLE deployment_events IS
    'Actual deployment fact source; releases and commits are code context only.';
COMMENT ON COLUMN deployment_events.source_time_text IS
    'Original source timestamp text retained for audit when timezone normalization occurs.';
COMMENT ON COLUMN data_source_mappings.secret_ref IS
    'Reference to a secret store entry only; never stores the secret value.';
COMMENT ON COLUMN alerts.redacted_payload IS
    'Source payload after credential and sensitive-field redaction.';
COMMENT ON TABLE diagnosis_cases IS
    'Evaluation answers and injection metadata; runtime tools must not query this table.';

-- +goose Down
DROP TABLE IF EXISTS investigation_transition_audits;
DROP TABLE IF EXISTS deployment_candidates;
DROP TABLE IF EXISTS notifications;
DROP TABLE IF EXISTS reports;
DROP TABLE IF EXISTS diagnosis_cases;
DROP TABLE IF EXISTS hypothesis_evidence;
DROP TABLE IF EXISTS hypotheses;
DROP TABLE IF EXISTS evidence;
DROP TABLE IF EXISTS tool_calls;
DROP TABLE IF EXISTS investigations;
DROP TABLE IF EXISTS incidents;
DROP TABLE IF EXISTS alerts;
DROP TABLE IF EXISTS data_source_mappings;
DROP TABLE IF EXISTS deployment_events;
DROP TABLE IF EXISTS releases;
DROP TABLE IF EXISTS services;
DROP TABLE IF EXISTS projects;

DROP FUNCTION IF EXISTS audit_investigation_transition();
DROP FUNCTION IF EXISTS enforce_investigation_transition();
DROP FUNCTION IF EXISTS reject_terminal_tool_call_mutation();
DROP FUNCTION IF EXISTS reject_mutation();
DROP FUNCTION IF EXISTS touch_updated_at();
