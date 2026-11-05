-- +goose Up
ALTER TABLE deployment_events
    ADD COLUMN event_version bigint NOT NULL DEFAULT 1 CHECK (event_version > 0),
    ADD COLUMN source_consistency text NOT NULL DEFAULT 'UNVERIFIED'
        CHECK (source_consistency IN ('MATCHED', 'SOURCE_CONFLICT', 'UNVERIFIED')),
    ADD COLUMN source_conflicts jsonb NOT NULL DEFAULT '[]'::jsonb
        CHECK (jsonb_typeof(source_conflicts) = 'array');

CREATE TABLE deployment_event_updates (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    deployment_event_id uuid NOT NULL REFERENCES deployment_events(id) ON DELETE RESTRICT,
    event_version bigint NOT NULL CHECK (event_version > 0),
    actor text NOT NULL CHECK (btrim(actor) <> '' AND length(actor) <= 256),
    request_id text NOT NULL CHECK (request_id ~ '^[A-Za-z0-9._:-]{1,128}$'),
    action text NOT NULL CHECK (action IN ('CREATED', 'UPDATED', 'CORRECTED')),
    reason text NOT NULL CHECK (btrim(reason) <> '' AND length(reason) <= 1024),
    before_state jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(before_state) = 'object'),
    after_state jsonb NOT NULL CHECK (jsonb_typeof(after_state) = 'object'),
    occurred_at timestamptz NOT NULL,
    UNIQUE (deployment_event_id, event_version)
);

CREATE INDEX deployment_event_updates_event_idx
    ON deployment_event_updates (deployment_event_id, event_version);

CREATE TABLE deployment_webhook_signatures (
    signature_digest bytea PRIMARY KEY CHECK (octet_length(signature_digest) = 32),
    expires_at timestamptz NOT NULL,
    claimed_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX deployment_webhook_signatures_expiry_idx
    ON deployment_webhook_signatures (expires_at);

CREATE TRIGGER deployment_event_updates_reject_mutation
BEFORE UPDATE OR DELETE ON deployment_event_updates
FOR EACH ROW EXECUTE FUNCTION reject_audit_mutation();

CREATE TRIGGER deployment_event_updates_reject_truncate
BEFORE TRUNCATE ON deployment_event_updates
FOR EACH STATEMENT EXECUTE FUNCTION reject_audit_mutation();

ALTER TABLE audit_events DROP CONSTRAINT audit_events_action_check;
ALTER TABLE audit_events ADD CONSTRAINT audit_events_action_check CHECK (action IN (
    'config.changed',
    'connection.tested',
    'webhook.received',
    'tool.called',
    'notification.sent',
    'report.generated',
    'deployment.event.changed'
));

COMMENT ON COLUMN deployment_events.event_version IS
    'Optimistic concurrency version for source corrections and status transitions.';
COMMENT ON COLUMN deployment_events.source_consistency IS
    'Comparison result between deployment-provided version/commit/digest and the referenced Release.';
COMMENT ON TABLE deployment_event_updates IS
    'Immutable audit history of deployment event creation, state transitions and manual corrections.';

-- +goose Down
ALTER TABLE audit_events DROP CONSTRAINT audit_events_action_check;
ALTER TABLE audit_events ADD CONSTRAINT audit_events_action_check CHECK (action IN (
    'config.changed',
    'connection.tested',
    'webhook.received',
    'tool.called',
    'notification.sent',
    'report.generated'
));
DROP TABLE IF EXISTS deployment_event_updates;
DROP TABLE IF EXISTS deployment_webhook_signatures;
ALTER TABLE deployment_events
    DROP COLUMN source_conflicts,
    DROP COLUMN source_consistency,
    DROP COLUMN event_version;
