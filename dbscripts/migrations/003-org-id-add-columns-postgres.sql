-- Org ID migration, step 1: add the org ID columns to an existing CDS database.
--
-- CDS keys its data by org ID instead of org handle. This script adds a nullable org_id column
-- next to each org_handle column, and nothing else: no NOT NULL, no index, no constraint. The
-- running server ignores the new columns, so this script can run against a live database. It
-- changes only the catalog, so it does not rewrite a table.
--
-- Run it before you set org_id_migration.phase to dual_write. It needs the organizations table
-- of 002-organization-management-postgres.sql. Read docs/guides/org-id-migration.md for the
-- full procedure.
--
-- Re-runnable. Every statement is guarded, so applying it twice is a no-op.
--
-- Rollback: drop the columns that this script adds.

BEGIN;

ALTER TABLE profiles                     ADD COLUMN IF NOT EXISTS org_id VARCHAR(255);
ALTER TABLE profile_reference            ADD COLUMN IF NOT EXISTS org_id VARCHAR(255);
ALTER TABLE profile_reference            ADD COLUMN IF NOT EXISTS reference_profile_org_id VARCHAR(255);
ALTER TABLE profile_schema               ADD COLUMN IF NOT EXISTS org_id VARCHAR(255);
ALTER TABLE unification_rules            ADD COLUMN IF NOT EXISTS org_id VARCHAR(255);
ALTER TABLE applications                 ADD COLUMN IF NOT EXISTS org_id VARCHAR(255);
ALTER TABLE consent_categories           ADD COLUMN IF NOT EXISTS org_id VARCHAR(255);
ALTER TABLE cds_config                   ADD COLUMN IF NOT EXISTS org_id VARCHAR(255);
ALTER TABLE profile_unification_modes    ADD COLUMN IF NOT EXISTS org_id VARCHAR(255);
ALTER TABLE profile_unification_triggers ADD COLUMN IF NOT EXISTS org_id VARCHAR(255);

COMMIT;
