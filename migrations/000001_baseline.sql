-- +goose Up
-- Reserve the first schema version for the runnable repository baseline.
-- Domain tables are introduced with their owning domain-model change.
SELECT 1;

-- +goose Down
SELECT 1;
