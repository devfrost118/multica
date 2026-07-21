-- Reconciles installs where 180_provider_limit_snapshots ran before daemon_id
-- was retroactively added to that CREATE TABLE (see f1194180c). On those
-- installs 180 is already marked applied, so the retroactive column never
-- lands; the following migration's index build then fails with "column
-- daemon_id does not exist". IF NOT EXISTS makes this a no-op on installs
-- where 180 already created the column.
ALTER TABLE provider_limit_snapshots ADD COLUMN IF NOT EXISTS daemon_id TEXT NOT NULL DEFAULT '';
