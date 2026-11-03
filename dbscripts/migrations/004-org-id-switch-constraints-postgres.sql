-- Org ID migration, step 4: move the constraints and indexes from org_handle to org_id.
--
-- Run it after the backfill (step 3) has filled org_id in every row: SET NOT NULL fails while a
-- row has no org ID. Servers in the dual_write phase keep running during and after this script.
-- After it, the read_org_id phase can start. Read docs/guides/org-id-migration.md for the full
-- procedure.
--
-- This script has no outer transaction: CREATE INDEX CONCURRENTLY cannot run in one. Run it with
--   psql -v ON_ERROR_STOP=1 -f 004-org-id-switch-constraints-postgres.sql
-- so that it stops at the first error.
--
-- Re-runnable. Every statement is guarded. If a CREATE INDEX CONCURRENTLY fails, it leaves an
-- INVALID index, which IF NOT EXISTS then skips: the last query of this script lists such indexes.
-- Drop each one and run the script again.
--
-- Rollback: drop the constraints and indexes that this script adds, restore the cds_config primary
-- key on (org_handle, config), and set org_handle NOT NULL again.

-- Do not wait behind a long query for a lock; fail, and run the script again later.
SET lock_timeout = '5s';

-- ── 1. org_id NOT NULL, without a scan under an exclusive lock ─────────────────────────────
--
-- A NOT VALID check constraint is added under a short lock. VALIDATE then scans the table under a
-- lock that does not block reads or writes. SET NOT NULL uses the valid constraint and does not
-- scan again (PostgreSQL 12 and later).

DO $$
DECLARE
    t text;
BEGIN
    FOREACH t IN ARRAY ARRAY['profiles', 'profile_reference', 'profile_schema', 'unification_rules',
        'applications', 'consent_categories', 'cds_config', 'profile_unification_modes',
        'profile_unification_triggers']
    LOOP
        IF EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema = current_schema()
                   AND table_name = t AND column_name = 'org_id' AND is_nullable = 'YES')
           AND NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = t || '_org_id_not_null') THEN
            EXECUTE format('ALTER TABLE %I ADD CONSTRAINT %I CHECK (org_id IS NOT NULL) NOT VALID',
                t, t || '_org_id_not_null');
        END IF;
    END LOOP;
END $$;

DO $$
DECLARE
    t text;
BEGIN
    FOREACH t IN ARRAY ARRAY['profiles', 'profile_reference', 'profile_schema', 'unification_rules',
        'applications', 'consent_categories', 'cds_config', 'profile_unification_modes',
        'profile_unification_triggers']
    LOOP
        IF EXISTS (SELECT 1 FROM pg_constraint WHERE conname = t || '_org_id_not_null' AND NOT convalidated) THEN
            EXECUTE format('ALTER TABLE %I VALIDATE CONSTRAINT %I', t, t || '_org_id_not_null');
        END IF;
    END LOOP;
END $$;

DO $$
DECLARE
    t text;
BEGIN
    FOREACH t IN ARRAY ARRAY['profiles', 'profile_reference', 'profile_schema', 'unification_rules',
        'applications', 'consent_categories', 'cds_config', 'profile_unification_modes',
        'profile_unification_triggers']
    LOOP
        IF EXISTS (SELECT 1 FROM pg_constraint WHERE conname = t || '_org_id_not_null') THEN
            EXECUTE format('ALTER TABLE %I ALTER COLUMN org_id SET NOT NULL', t);
            EXECUTE format('ALTER TABLE %I DROP CONSTRAINT %I', t, t || '_org_id_not_null');
        END IF;
    END LOOP;
END $$;

-- profile_reference.reference_profile_org_id stays nullable, as reference_profile_org_handle is.

-- ── 2. Indexes on org_id ───────────────────────────────────────────────────────────────────
--
-- The names are the names of dbscripts/postgres.sql, so a migrated database and a fresh install
-- match, and step 7 needs no rename.

CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_profiles_org_id_created_profile
    ON profiles (org_id, created_at, profile_id);
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_profiles_org_id_user
    ON profiles (org_id, user_id);
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_profile_reference_org_id_status_profile
    ON profile_reference (org_id, profile_status, profile_id);
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_profile_schema_org_id_scope
    ON profile_schema (org_id, scope);
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_profile_schema_org_id_attr_name
    ON profile_schema (org_id, attribute_name);
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_unification_rules_org_id_active_priority
    ON unification_rules (org_id, is_active, priority);

-- ── 3. Unique keys on org_id ───────────────────────────────────────────────────────────────
--
-- Each key is built as a unique index without a write lock, then attached as a constraint.

CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS applications_org_id_client_id_key
    ON applications (org_id, client_id);
CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS profile_unification_modes_org_id_merge_type_rule_key
    ON profile_unification_modes (org_id, merge_type, rule);
CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS profile_unification_triggers_org_id_key
    ON profile_unification_triggers (org_id);

DO $$
DECLARE
    k record;
BEGIN
    FOR k IN SELECT * FROM (VALUES
        ('applications', 'applications_org_id_client_id_key'),
        ('profile_unification_modes', 'profile_unification_modes_org_id_merge_type_rule_key'),
        ('profile_unification_triggers', 'profile_unification_triggers_org_id_key')) AS v(tbl, name)
    LOOP
        IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = k.name) THEN
            EXECUTE format('ALTER TABLE %I ADD CONSTRAINT %I UNIQUE USING INDEX %I', k.tbl, k.name, k.name);
        END IF;
    END LOOP;
END $$;

-- ── 4. cds_config: primary key on (org_id, config) ────────────────────────────────────────
--
-- Servers in the dual_write phase upsert with ON CONFLICT (org_handle, config), so a unique index
-- on it stays until step 7.

CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS cds_config_org_handle_config_key
    ON cds_config (org_handle, config);
CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS cds_config_org_id_config_idx
    ON cds_config (org_id, config);

DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_index ix
               JOIN pg_class i ON i.oid = ix.indexrelid
               JOIN pg_attribute a ON a.attrelid = ix.indrelid AND a.attnum = ix.indkey[0]
               WHERE ix.indrelid = 'cds_config'::regclass AND ix.indisprimary AND a.attname = 'org_handle') THEN
        ALTER TABLE cds_config DROP CONSTRAINT cds_config_pkey;
        ALTER TABLE cds_config ADD CONSTRAINT cds_config_pkey PRIMARY KEY USING INDEX cds_config_org_id_config_idx;
    END IF;
END $$;

-- The swap renames the index to cds_config_pkey. On a second run of this script, the CREATE INDEX
-- above builds it again under its own name, next to the primary key. Drop that copy.
DROP INDEX CONCURRENTLY IF EXISTS cds_config_org_id_config_idx;

-- ── 5. org_handle nullable ─────────────────────────────────────────────────────────────────
--
-- Servers in the org_id_only phase do not write org_handle.

DO $$
DECLARE
    t text;
BEGIN
    FOREACH t IN ARRAY ARRAY['profile_reference', 'profile_schema', 'unification_rules', 'applications',
        'consent_categories', 'cds_config', 'profile_unification_modes', 'profile_unification_triggers']
    LOOP
        IF EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema = current_schema()
                   AND table_name = t AND column_name = 'org_handle' AND is_nullable = 'NO') THEN
            EXECUTE format('ALTER TABLE %I ALTER COLUMN org_handle DROP NOT NULL', t);
        END IF;
    END LOOP;
END $$;

RESET lock_timeout;

-- ── 6. Check ───────────────────────────────────────────────────────────────────────────────
--
-- Expected: no rows. A row is an index that a failed CREATE INDEX CONCURRENTLY left INVALID. Drop it
-- (DROP INDEX CONCURRENTLY <name>) and run this script again.

SELECT t.relname AS table_name, i.relname AS invalid_index
FROM pg_index ix
JOIN pg_class i ON i.oid = ix.indexrelid
JOIN pg_class t ON t.oid = ix.indrelid
JOIN pg_namespace n ON n.oid = t.relnamespace
WHERE n.nspname = current_schema() AND NOT ix.indisvalid;
