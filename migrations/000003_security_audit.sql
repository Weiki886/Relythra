-- +goose Up
CREATE TABLE audit_events (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    actor text NOT NULL CHECK (btrim(actor) <> '' AND length(actor) <= 256),
    action text NOT NULL CHECK (action IN (
        'config.changed',
        'connection.tested',
        'webhook.received',
        'tool.called',
        'notification.sent',
        'report.generated'
    )),
    object_type text NOT NULL CHECK (
        btrim(object_type) <> '' AND length(object_type) <= 128
    ),
    object_id text NOT NULL CHECK (
        btrim(object_id) <> '' AND length(object_id) <= 256
    ),
    result text NOT NULL CHECK (result IN ('SUCCEEDED', 'FAILED', 'DENIED')),
    request_id text NOT NULL CHECK (request_id ~ '^[A-Za-z0-9._:-]{1,128}$'),
    parameters jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (
        jsonb_typeof(parameters) = 'object'
        AND octet_length(parameters::text) <= 65536
    ),
    occurred_at timestamptz NOT NULL,
    recorded_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX audit_events_object_idx
    ON audit_events (object_type, object_id, occurred_at DESC);
CREATE INDEX audit_events_request_idx
    ON audit_events (request_id, occurred_at DESC);
CREATE INDEX audit_events_actor_action_idx
    ON audit_events (actor, action, occurred_at DESC);

-- +goose StatementBegin
CREATE FUNCTION reject_audit_mutation()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    RAISE EXCEPTION 'audit events are immutable'
        USING ERRCODE = '23001';
END;
$$;
-- +goose StatementEnd

CREATE TRIGGER audit_events_reject_mutation
BEFORE UPDATE OR DELETE ON audit_events
FOR EACH ROW EXECUTE FUNCTION reject_audit_mutation();

CREATE TRIGGER audit_events_reject_truncate
BEFORE TRUNCATE ON audit_events
FOR EACH STATEMENT EXECUTE FUNCTION reject_audit_mutation();

COMMENT ON TABLE audit_events IS
    'Append-only security audit trail; parameters must be redacted before insertion.';
COMMENT ON COLUMN audit_events.parameters IS
    'Redacted operation summary; secret values and credential-bearing URLs are prohibited.';

-- +goose Down
DROP TABLE IF EXISTS audit_events;
DROP FUNCTION IF EXISTS reject_audit_mutation();
