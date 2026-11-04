-- Identity resolution: schema delta for an existing CDS database.
--
-- Brings a database created before identity resolution up to the schema in
-- dbscripts/postgres.sql, and nothing else. Purely additive: no table, column, index or row
-- is dropped or rewritten, so it can run against a live database.
--
-- Re-runnable. Every statement is guarded, so applying it twice is a no-op.

BEGIN;

-- ── unification_rules: how a rule compares values ──────────────────────────────────────
--
-- attribute_type and unification_method carry defaults on purpose: a rule written before
-- typed matching is an exact match on a plain value, which is what the engine applied then
-- and must keep applying now.
ALTER TABLE unification_rules
    ADD COLUMN IF NOT EXISTS attribute_type     VARCHAR(255) NOT NULL DEFAULT 'PRIMITIVE_EXACT';
ALTER TABLE unification_rules
    ADD COLUMN IF NOT EXISTS unification_method VARCHAR(255) NOT NULL DEFAULT 'deterministic';

-- The strengths are deliberately nullable WITH NO DEFAULT. A default here would stamp a
-- concrete value onto every existing rule, which the engine cannot tell apart from an
-- operator's deliberate override — it would stop deriving the strength from attribute_type
-- and silently change which profiles merge. Leave these exactly as they are.
ALTER TABLE unification_rules
    ADD COLUMN IF NOT EXISTS match_strength     VARCHAR(20);
ALTER TABLE unification_rules
    ADD COLUMN IF NOT EXISTS mismatch_strength  VARCHAR(20);

-- ── blocking_keys: the candidate index ─────────────────────────────────────────────────
--
-- Hash partitioned on org_handle, and again on key_value beneath it. The moduli cannot be
-- changed later without rebuilding the table.
CREATE TABLE IF NOT EXISTS blocking_keys (
    profile_id      VARCHAR(255) NOT NULL REFERENCES profiles(profile_id) ON DELETE CASCADE,
    org_handle      VARCHAR(255) NOT NULL,
    attribute_name  VARCHAR(255) NOT NULL,
    key_value       VARCHAR(512) NOT NULL,
    PRIMARY KEY (org_handle, attribute_name, key_value, profile_id)
) PARTITION BY HASH (org_handle);

DO $$
BEGIN
    FOR i IN 0..7 LOOP
        EXECUTE format(
            'CREATE TABLE IF NOT EXISTS blocking_keys_p%s PARTITION OF blocking_keys '
            'FOR VALUES WITH (MODULUS 8, REMAINDER %s) PARTITION BY HASH (key_value)', i, i);
        FOR j IN 0..3 LOOP
            EXECUTE format(
                'CREATE TABLE IF NOT EXISTS blocking_keys_p%s_k%s PARTITION OF blocking_keys_p%s '
                'FOR VALUES WITH (MODULUS 4, REMAINDER %s)', i, j, i, j);
        END LOOP;
    END LOOP;
END $$;

CREATE INDEX IF NOT EXISTS idx_blocking_keys_org_profile ON blocking_keys(org_handle, profile_id);

-- ── review_tasks: pairs awaiting an administrator's decision ───────────────────────────
CREATE TABLE IF NOT EXISTS review_tasks (
    id                     VARCHAR(255) PRIMARY KEY,
    org_handle             VARCHAR(255) NOT NULL,
    incoming_profile_id    VARCHAR(255) NOT NULL REFERENCES profiles(profile_id) ON DELETE CASCADE,
    candidate_profile_id   VARCHAR(255) NOT NULL REFERENCES profiles(profile_id) ON DELETE CASCADE,
    match_score            DECIMAL(5,4) NOT NULL,
    status                 VARCHAR(50)  NOT NULL DEFAULT 'PENDING',
    match_reason           TEXT,
    score_breakdown        JSONB DEFAULT '{}'::jsonb,
    created_at             TIMESTAMPTZ  NOT NULL DEFAULT now(),
    resolved_at            TIMESTAMPTZ,
    resolved_by            VARCHAR(255),
    resolution_notes       TEXT,
    CONSTRAINT uq_review_task_profiles UNIQUE (incoming_profile_id, candidate_profile_id)
);

CREATE INDEX IF NOT EXISTS idx_review_tasks_org_status ON review_tasks(org_handle, status);
CREATE INDEX IF NOT EXISTS idx_review_tasks_created   ON review_tasks(created_at DESC);
CREATE INDEX IF NOT EXISTS idx_review_tasks_incoming  ON review_tasks(incoming_profile_id, status);

-- ── rejection_pairs: pairs an administrator ruled are different people ─────────────────
CREATE TABLE IF NOT EXISTS rejection_pairs (
    id              VARCHAR(255) PRIMARY KEY,
    org_handle      VARCHAR(255) NOT NULL,
    profile_id_1    VARCHAR(255) NOT NULL,
    profile_id_2    VARCHAR(255) NOT NULL,
    match_score     DECIMAL(5,4),
    score_breakdown JSONB DEFAULT '{}'::jsonb,
    rejected_by     VARCHAR(255),
    rejected_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT uq_rejection_pair UNIQUE (profile_id_1, profile_id_2)
);

CREATE INDEX IF NOT EXISTS idx_rejection_pairs_p1 ON rejection_pairs(org_handle, profile_id_1);
CREATE INDEX IF NOT EXISTS idx_rejection_pairs_p2 ON rejection_pairs(org_handle, profile_id_2);

COMMIT;
