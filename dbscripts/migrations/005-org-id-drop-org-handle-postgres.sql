-- Org ID migration, step 7: drop the org handle columns.
--
-- Run it only after every server uses org_id_migration.phase org_id_only. No statement of that
-- phase names these columns. After this script, the dual_write and read_org_id phases refuse to
-- start.
--
-- Back up the database first. This script cannot be rolled back: restore the backup instead.
--
-- This script has no outer transaction: DROP INDEX CONCURRENTLY cannot run in one. Run it with
--   psql -v ON_ERROR_STOP=1 -f 005-org-id-drop-org-handle-postgres.sql
--
-- Re-runnable. Every statement is guarded.

SET lock_timeout = '5s';

-- ── 1. Indexes and keys on org_handle ──────────────────────────────────────────────────────
--
-- DROP COLUMN would also drop them, but under its exclusive lock. Drop them first, without a
-- write lock.

DROP INDEX CONCURRENTLY IF EXISTS idx_profiles_org_created_profile;
DROP INDEX CONCURRENTLY IF EXISTS idx_profiles_org_user;
DROP INDEX CONCURRENTLY IF EXISTS idx_profile_reference_org_status_profile;
DROP INDEX CONCURRENTLY IF EXISTS idx_profile_schema_org_scope;
DROP INDEX CONCURRENTLY IF EXISTS idx_profile_schema_org_attr_name;
DROP INDEX CONCURRENTLY IF EXISTS idx_unification_rules_org_active_priority;
DROP INDEX CONCURRENTLY IF EXISTS cds_config_org_handle_config_key;
DROP INDEX CONCURRENTLY IF EXISTS idx_review_tasks_org_status;
DROP INDEX CONCURRENTLY IF EXISTS idx_rejection_pairs_p1;
DROP INDEX CONCURRENTLY IF EXISTS idx_rejection_pairs_p2;

-- PostgreSQL cannot drop an index of a partitioned table concurrently. These drops lock
-- blocking_keys for a short time.
DROP INDEX IF EXISTS idx_blocking_keys_org_handle_profile;
DROP INDEX IF EXISTS idx_blocking_keys_org_handle_key;

ALTER TABLE applications DROP CONSTRAINT IF EXISTS applications_org_handle_client_id_key;
ALTER TABLE profile_unification_modes DROP CONSTRAINT IF EXISTS profile_unification_modes_org_handle_merge_type_rule_key;
ALTER TABLE profile_unification_triggers DROP CONSTRAINT IF EXISTS profile_unification_triggers_org_handle_key;

-- ── 2. The columns ─────────────────────────────────────────────────────────────────────────
--
-- DROP COLUMN changes only the catalog; it does not rewrite the table.

ALTER TABLE profiles                     DROP COLUMN IF EXISTS org_handle;
ALTER TABLE profile_reference            DROP COLUMN IF EXISTS org_handle;
ALTER TABLE profile_reference            DROP COLUMN IF EXISTS reference_profile_org_handle;
ALTER TABLE profile_schema               DROP COLUMN IF EXISTS org_handle;
ALTER TABLE unification_rules            DROP COLUMN IF EXISTS org_handle;
ALTER TABLE applications                 DROP COLUMN IF EXISTS org_handle;
ALTER TABLE consent_categories           DROP COLUMN IF EXISTS org_handle;
ALTER TABLE cds_config                   DROP COLUMN IF EXISTS org_handle;
ALTER TABLE profile_unification_modes    DROP COLUMN IF EXISTS org_handle;
ALTER TABLE profile_unification_triggers DROP COLUMN IF EXISTS org_handle;
ALTER TABLE blocking_keys                DROP COLUMN IF EXISTS org_handle;
ALTER TABLE review_tasks                 DROP COLUMN IF EXISTS org_handle;
ALTER TABLE rejection_pairs              DROP COLUMN IF EXISTS org_handle;

RESET lock_timeout;
