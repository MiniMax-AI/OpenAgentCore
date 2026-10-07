-- +goose Up
-- A Session's binding to its Runtime becomes a fenced assignment. Core
-- advances epoch with each change of desired_state; applied_epoch is the
-- latest released epoch the Runtime acknowledged.
ALTER TABLE session_devices RENAME TO session_runtime_assignments;
ALTER TABLE session_runtime_assignments RENAME COLUMN device_id TO runtime_id;
ALTER INDEX session_devices_pkey RENAME TO session_runtime_assignments_pkey;
ALTER TABLE session_runtime_assignments RENAME CONSTRAINT session_devices_session_id_fkey TO session_runtime_assignments_session_id_fkey;
ALTER TABLE session_runtime_assignments RENAME CONSTRAINT session_devices_device_id_fkey TO session_runtime_assignments_runtime_id_fkey;
ALTER TABLE session_runtime_assignments
  ADD COLUMN assignment_id uuid NOT NULL DEFAULT gen_random_uuid() UNIQUE,
  ADD COLUMN epoch bigint NOT NULL DEFAULT 1 CHECK (epoch > 0),
  ADD COLUMN desired_state text NOT NULL DEFAULT 'bound' CHECK (desired_state IN ('bound', 'released')),
  ADD COLUMN remove_home boolean NOT NULL DEFAULT false,
  ADD COLUMN applied_epoch bigint NOT NULL DEFAULT 0,
  ADD CONSTRAINT session_runtime_assignments_applied CHECK (applied_epoch BETWEEN 0 AND epoch),
  ADD CONSTRAINT session_runtime_assignments_release CHECK (desired_state = 'released' OR NOT remove_home);
-- A deleted Session owes its Runtime the release that removes its home.
UPDATE session_runtime_assignments a SET desired_state = 'released', remove_home = true, epoch = 2
FROM sessions s WHERE s.id = a.session_id AND s.deleted_at IS NOT NULL;
CREATE INDEX session_runtime_assignments_pending_idx ON session_runtime_assignments (runtime_id, session_id)
  WHERE desired_state = 'released' AND applied_epoch < epoch;

-- +goose Down
DROP INDEX session_runtime_assignments_pending_idx;
ALTER TABLE session_runtime_assignments
  DROP CONSTRAINT session_runtime_assignments_release,
  DROP CONSTRAINT session_runtime_assignments_applied,
  DROP COLUMN applied_epoch,
  DROP COLUMN remove_home,
  DROP COLUMN desired_state,
  DROP COLUMN epoch,
  DROP COLUMN assignment_id;
ALTER TABLE session_runtime_assignments RENAME CONSTRAINT session_runtime_assignments_runtime_id_fkey TO session_devices_device_id_fkey;
ALTER TABLE session_runtime_assignments RENAME CONSTRAINT session_runtime_assignments_session_id_fkey TO session_devices_session_id_fkey;
ALTER INDEX session_runtime_assignments_pkey RENAME TO session_devices_pkey;
ALTER TABLE session_runtime_assignments RENAME COLUMN runtime_id TO device_id;
ALTER TABLE session_runtime_assignments RENAME TO session_devices;
