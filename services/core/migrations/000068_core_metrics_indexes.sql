-- +goose Up
CREATE INDEX turns_metrics_started_idx ON turns(started_at) INCLUDE (created_at)
    WHERE started_at IS NOT NULL;
CREATE INDEX turns_metrics_interrupted_idx ON turns(completed_at)
    WHERE status = 'failed' AND outcome->>'error_code' = 'execution_interrupted';

-- +goose Down
DROP INDEX turns_metrics_interrupted_idx;
DROP INDEX turns_metrics_started_idx;
