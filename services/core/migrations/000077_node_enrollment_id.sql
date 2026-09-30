-- +goose Up
-- A public, non-secret handle for each node enrollment command, so the console can
-- tell which node a command registered. It never authenticates; the token does.
-- Tokens issued before this migration have none.
ALTER TABLE runtime_node_enrollments ADD COLUMN id uuid UNIQUE;
-- The enrollment that registered the node, copied at registration. There is no
-- foreign key: runtime_node_enrollments.node_id already references the node, and a
-- reverse key would only add a cycle. Nodes enrolled earlier keep null.
ALTER TABLE runtime_nodes ADD COLUMN enrollment_id uuid;

-- +goose Down
ALTER TABLE runtime_nodes DROP COLUMN enrollment_id;
ALTER TABLE runtime_node_enrollments DROP COLUMN id;
