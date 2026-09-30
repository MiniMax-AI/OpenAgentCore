-- +goose Up
-- Only the archive transaction that first revokes the device may retain the
-- existing delivery's cancellation receipt. Old revocations are not adopted.
ALTER TABLE devices
    ADD COLUMN archive_cancel_turn_id uuid REFERENCES turns(id) ON DELETE SET NULL;

-- +goose Down
-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM devices d JOIN runtime_allocations a ON a.device_id = d.id
               WHERE d.archive_cancel_turn_id IS NOT NULL AND a.state <> 'released') THEN
        RAISE EXCEPTION 'Cannot remove archived cancellation receipts while cleanup is unsettled';
    END IF;
END $$;
-- +goose StatementEnd
ALTER TABLE devices DROP COLUMN archive_cancel_turn_id;
