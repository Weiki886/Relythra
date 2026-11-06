-- +goose Up
ALTER TABLE alerts ADD COLUMN fingerprint_version text NOT NULL DEFAULT 'grafana-v1'
    CHECK (btrim(fingerprint_version) <> '');
ALTER TABLE alerts ADD COLUMN event_version bigint NOT NULL DEFAULT 1 CHECK (event_version > 0);
ALTER TABLE alerts ADD CONSTRAINT alerts_id_project_unique UNIQUE (id, project_id);
ALTER TABLE incidents ADD COLUMN alert_id uuid;
ALTER TABLE incidents ADD CONSTRAINT incidents_alert_unique UNIQUE (alert_id);
ALTER TABLE incidents ADD CONSTRAINT incidents_alert_project_fk
    FOREIGN KEY (alert_id, project_id) REFERENCES alerts(id, project_id) ON DELETE RESTRICT;
ALTER TABLE audit_events DROP CONSTRAINT audit_events_action_check;
ALTER TABLE audit_events ADD CONSTRAINT audit_events_action_check CHECK (action IN (
    'config.changed', 'connection.tested', 'webhook.received', 'tool.called',
    'notification.sent', 'report.generated', 'deployment.event.changed', 'alert.mapping.changed'
));

CREATE TABLE alert_deliveries (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    project_id uuid NOT NULL REFERENCES projects(id) ON DELETE RESTRICT,
    alert_id uuid NOT NULL,
    request_id text NOT NULL CHECK (request_id ~ '^[A-Za-z0-9._:-]{1,128}$'),
    delivery_status text NOT NULL CHECK (delivery_status IN ('FIRING', 'RESOLVED')),
    mapping_status text NOT NULL CHECK (mapping_status IN ('MATCHED', 'NEEDS_MAPPING')),
    payload_hash text NOT NULL CHECK (payload_hash ~ '^[0-9a-f]{64}$'),
    redacted_payload jsonb NOT NULL CHECK (jsonb_typeof(redacted_payload) = 'object'),
    received_at timestamptz NOT NULL,
    FOREIGN KEY (alert_id, project_id) REFERENCES alerts(id, project_id) ON DELETE RESTRICT
);

CREATE INDEX alert_deliveries_alert_received_idx
    ON alert_deliveries (alert_id, received_at DESC);

CREATE TRIGGER alert_deliveries_reject_mutation
BEFORE UPDATE OR DELETE ON alert_deliveries
FOR EACH ROW EXECUTE FUNCTION reject_audit_mutation();

CREATE TRIGGER alert_deliveries_reject_truncate
BEFORE TRUNCATE ON alert_deliveries
FOR EACH STATEMENT EXECUTE FUNCTION reject_audit_mutation();

COMMENT ON TABLE alert_deliveries IS
    'Append-only redacted history of each accepted Grafana alert delivery.';

-- +goose Down
DROP TABLE IF EXISTS alert_deliveries;
ALTER TABLE incidents DROP CONSTRAINT incidents_alert_project_fk;
ALTER TABLE incidents DROP CONSTRAINT incidents_alert_unique;
ALTER TABLE incidents DROP COLUMN alert_id;
ALTER TABLE alerts DROP CONSTRAINT alerts_id_project_unique;
ALTER TABLE audit_events DROP CONSTRAINT audit_events_action_check;
ALTER TABLE audit_events ADD CONSTRAINT audit_events_action_check CHECK (action IN (
    'config.changed', 'connection.tested', 'webhook.received', 'tool.called',
    'notification.sent', 'report.generated', 'deployment.event.changed'
));
ALTER TABLE alerts DROP COLUMN event_version;
ALTER TABLE alerts DROP COLUMN fingerprint_version;
