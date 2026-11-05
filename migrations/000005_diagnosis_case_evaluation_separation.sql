-- +goose Up
-- Answer-bearing rows cannot be safely transformed into versioned files by SQL.
-- Stop rather than deleting evaluation truth; export it to the protected
-- test/evaluation tree before retrying this migration.
-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM diagnosis_cases) THEN
        RAISE EXCEPTION 'diagnosis_cases contains rows; export and remove evaluation cases before applying migration 000005';
    END IF;
END;
$$;
-- +goose StatementEnd

ALTER TABLE diagnosis_cases
    DROP COLUMN injection_method,
    DROP COLUMN expected_root_cause,
    DROP COLUMN allowed_variants,
    DROP COLUMN required_evidence,
    DROP COLUMN counterexample_conditions,
    DROP COLUMN healthy_baseline,
    DROP COLUMN scenario_metadata;

COMMENT ON TABLE diagnosis_cases IS
    'Runtime-safe case catalog metadata only; evaluation truth and injection state are stored outside the runtime database.';

-- +goose Down
ALTER TABLE diagnosis_cases
    ADD COLUMN injection_method jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(injection_method) = 'object'),
    ADD COLUMN expected_root_cause jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(expected_root_cause) = 'object'),
    ADD COLUMN allowed_variants jsonb NOT NULL DEFAULT '[]'::jsonb CHECK (jsonb_typeof(allowed_variants) = 'array'),
    ADD COLUMN required_evidence jsonb NOT NULL DEFAULT '[]'::jsonb CHECK (jsonb_typeof(required_evidence) = 'array'),
    ADD COLUMN counterexample_conditions jsonb NOT NULL DEFAULT '[]'::jsonb CHECK (jsonb_typeof(counterexample_conditions) = 'array'),
    ADD COLUMN healthy_baseline jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(healthy_baseline) = 'object'),
    ADD COLUMN scenario_metadata jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(scenario_metadata) = 'object');

COMMENT ON TABLE diagnosis_cases IS
    'Evaluation answers and injection metadata; runtime tools must not query this table.';
