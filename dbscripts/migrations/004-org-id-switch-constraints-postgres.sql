-- Org ID migration, step 4: move the constraints and indexes from org_handle to org_id.
--
-- Run it after the backfill (step 3) has filled org_id in every row: SET NOT NULL fails while a
-- row has no org ID. Servers in the dual_write phase keep running during and after this script.
-- After it, the read_org_id phase can start.
--
-- This script has no outer transaction: CREATE INDEX CONCURRENTLY cannot run in one. Run it with
--   psql -v ON_ERROR_STOP=1 -f 004-org-id-switch-constraints-postgres.sql
-- so that it stops at the first error.
--
-- Re-runnable. Every statement is guarded. If a CREATE INDEX CONCURRENTLY fails, it leaves an
-- INVALID index, which IF NOT EXISTS then skips: the last query of this script lists such indexes.
-- Drop each one and run the script again.
--
-- Caution: section 5 stops the writes to blocking_keys while it copies the table. Read it before you
-- run this script.
--
-- Rollback: drop the constraints and indexes that this script adds, restore the cds_config primary
-- key on (org_handle, config), and set org_handle NOT NULL again. The old blocking_keys is dropped:
-- to go back, rebuild it partitioned by org_handle from the new table.

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
        'profile_unification_triggers', 'review_tasks', 'rejection_pairs']
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
        'profile_unification_triggers', 'review_tasks', 'rejection_pairs']
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
        'profile_unification_triggers', 'review_tasks', 'rejection_pairs']
    LOOP
        IF EXISTS (SELECT 1 FROM pg_constraint WHERE conname = t || '_org_id_not_null') THEN
            EXECUTE format('ALTER TABLE %I ALTER COLUMN org_id SET NOT NULL', t);
            EXECUTE format('ALTER TABLE %I DROP CONSTRAINT %I', t, t || '_org_id_not_null');
        END IF;
    END LOOP;
END $$;

-- profile_reference.reference_profile_org_id stays nullable, as reference_profile_org_handle is.
-- blocking_keys gets its NOT NULL org_id from the rebuild in section 5.

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
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_review_tasks_org_id_status
    ON review_tasks (org_id, status);
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_rejection_pairs_org_id_p1
    ON rejection_pairs (org_id, profile_id_1);
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_rejection_pairs_org_id_p2
    ON rejection_pairs (org_id, profile_id_2);

-- ── 3. Unique keys on org_id ───────────────────────────────────────────────────────────────
--
-- Each key is built as a unique index without a write lock, then attached as a constraint. The
-- names are the names that PostgreSQL gives the unnamed keys of dbscripts/postgres.sql.

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

-- ── 5. blocking_keys: rebuild it partitioned by org_id ─────────────────────────────────────
--
-- blocking_keys is partitioned by org_handle, and PostgreSQL cannot change a partition key. So a
-- new table, partitioned by org_id, replaces it. The new table keeps a nullable org_handle and two
-- indexes on it, because servers in the dual_write phase write and read by org_handle. Step 7
-- drops them.
--
-- The first block creates the new table, empty. It is a transaction of its own: its foreign key
-- locks profiles only for the short time that an empty table needs.

DO $$
DECLARE
    i int;
    j int;
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_partitioned_table pt
                   JOIN pg_class c ON c.oid = pt.partrelid
                   JOIN pg_attribute a ON a.attrelid = c.oid AND a.attnum = pt.partattrs[0]
                   WHERE c.relname = 'blocking_keys' AND c.relnamespace = current_schema()::regnamespace
                     AND a.attname = 'org_handle') THEN
        RETURN;
    END IF;

    CREATE TABLE IF NOT EXISTS blocking_keys_org_id (
        profile_id     VARCHAR(255) NOT NULL,
        org_handle     VARCHAR(255),
        attribute_name VARCHAR(255) NOT NULL,
        key_value      VARCHAR(512) NOT NULL,
        org_id         VARCHAR(255) NOT NULL,
        CONSTRAINT blocking_keys_profile_id_fkey FOREIGN KEY (profile_id)
            REFERENCES profiles (profile_id) ON DELETE CASCADE,
        PRIMARY KEY (org_id, attribute_name, key_value, profile_id)
    ) PARTITION BY HASH (org_id);

    -- The same partitions as dbscripts/postgres.sql: 8 by org_id, each 4 by key_value.
    FOR i IN 0..7 LOOP
        EXECUTE format(
            'CREATE TABLE IF NOT EXISTS blocking_keys_org_id_p%s PARTITION OF blocking_keys_org_id '
            'FOR VALUES WITH (MODULUS 8, REMAINDER %s) PARTITION BY HASH (key_value)', i, i);
        FOR j IN 0..3 LOOP
            EXECUTE format(
                'CREATE TABLE IF NOT EXISTS blocking_keys_org_id_p%s_k%s PARTITION OF blocking_keys_org_id_p%s '
                'FOR VALUES WITH (MODULUS 4, REMAINDER %s)', i, j, i, j);
        END LOOP;
    END LOOP;

    CREATE INDEX IF NOT EXISTS idx_blocking_keys_org_id_profile ON blocking_keys_org_id (org_id, profile_id);
    CREATE INDEX IF NOT EXISTS idx_blocking_keys_org_handle_profile ON blocking_keys_org_id (org_handle, profile_id);
    CREATE INDEX IF NOT EXISTS idx_blocking_keys_org_handle_key
        ON blocking_keys_org_id (org_handle, attribute_name, key_value);
END $$;

-- The second block copies the rows and swaps the tables, in one transaction.
--
-- Caution: during the copy, writes to blocking_keys wait. The identity resolution worker and
-- profile deletes (which cascade to blocking_keys) wait until the block ends. Reads continue
-- until the swap at the end. Run this section at a time of low traffic.
--
-- A row whose org_handle has no organizations row is not copied. Blocking keys are derived from
-- the profiles, so a re-index makes them again.
--
-- If the block stops on a lock timeout or a deadlock (a profile delete during the copy), the
-- transaction rolls back and the old table stays. Run the script again.

DO $$
DECLARE
    r record;
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_partitioned_table pt
                   JOIN pg_class c ON c.oid = pt.partrelid
                   JOIN pg_attribute a ON a.attrelid = c.oid AND a.attnum = pt.partattrs[0]
                   WHERE c.relname = 'blocking_keys' AND c.relnamespace = current_schema()::regnamespace
                     AND a.attname = 'org_handle') THEN
        RETURN;
    END IF;

    LOCK TABLE blocking_keys IN EXCLUSIVE MODE;

    INSERT INTO blocking_keys_org_id (profile_id, org_handle, attribute_name, key_value, org_id)
    SELECT b.profile_id, b.org_handle, b.attribute_name, b.key_value, o.org_id
    FROM blocking_keys b
    JOIN organizations o ON o.org_handle = b.org_handle;

    DROP TABLE blocking_keys;

    -- Give the new table, its partitions and their indexes the names of dbscripts/postgres.sql.
    -- A primary key follows the name of its index.
    FOR r IN SELECT c.relname, c.relkind FROM pg_class c
             WHERE c.relnamespace = current_schema()::regnamespace
               AND c.relname LIKE 'blocking\_keys\_org\_id%'
             ORDER BY c.relkind DESC
    LOOP
        IF r.relkind IN ('r', 'p') THEN
            EXECUTE format('ALTER TABLE %I RENAME TO %I', r.relname,
                regexp_replace(r.relname, '^blocking_keys_org_id', 'blocking_keys'));
        ELSE
            EXECUTE format('ALTER INDEX %I RENAME TO %I', r.relname,
                regexp_replace(r.relname, '^blocking_keys_org_id', 'blocking_keys'));
        END IF;
    END LOOP;
END $$;

-- ── 6. org_handle nullable ─────────────────────────────────────────────────────────────────
--
-- Servers in the org_id_only phase do not write org_handle.

DO $$
DECLARE
    t text;
BEGIN
    FOREACH t IN ARRAY ARRAY['profile_reference', 'profile_schema', 'unification_rules', 'applications',
        'consent_categories', 'cds_config', 'profile_unification_modes', 'profile_unification_triggers',
        'review_tasks', 'rejection_pairs']
    LOOP
        IF EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema = current_schema()
                   AND table_name = t AND column_name = 'org_handle' AND is_nullable = 'NO') THEN
            EXECUTE format('ALTER TABLE %I ALTER COLUMN org_handle DROP NOT NULL', t);
        END IF;
    END LOOP;
END $$;

RESET lock_timeout;

-- ── 7. Check ───────────────────────────────────────────────────────────────────────────────
--
-- Expected: no rows. A row is an index that a failed CREATE INDEX CONCURRENTLY left INVALID. Drop it
-- (DROP INDEX CONCURRENTLY <name>) and run this script again.

SELECT t.relname AS table_name, i.relname AS invalid_index
FROM pg_index ix
JOIN pg_class i ON i.oid = ix.indexrelid
JOIN pg_class t ON t.oid = ix.indrelid
JOIN pg_namespace n ON n.oid = t.relnamespace
WHERE n.nspname = current_schema() AND NOT ix.indisvalid;
