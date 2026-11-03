-- The install schema from before the org ID migration (dbscripts/postgres.sql at that time). The
-- migration tests build a legacy database from it, then apply dbscripts/migrations/003 to 005.

CREATE TABLE profile_unification_modes
(
    id         SERIAL PRIMARY KEY,
    org_handle  VARCHAR(255) NOT NULL,
    merge_type VARCHAR(255) NOT NULL,
    rule       VARCHAR(255) NOT NULL,
    UNIQUE (org_handle, merge_type, rule)
);

CREATE TABLE profile_unification_triggers
(
    id           SERIAL PRIMARY KEY,
    org_handle    VARCHAR(255) NOT NULL UNIQUE,
    trigger_type VARCHAR(255) NOT NULL,
    last_trigger BIGINT DEFAULT 0,
    duration     BIGINT DEFAULT 0
);

-- Profiles Table
CREATE TABLE profiles
(
    profile_id          VARCHAR(255) PRIMARY KEY,
    user_id             VARCHAR(255),
    org_handle           VARCHAR(255),
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    location            VARCHAR(255),
    origin_country      VARCHAR(255),
    list_profile        BOOLEAN DEFAULT TRUE,
    delete_profile      BOOLEAN DEFAULT FALSE,
    traits              JSONB   DEFAULT '{}'::jsonb,
    identity_attributes JSONB   DEFAULT '{}'::jsonb
);

CREATE TABLE profile_reference
(
    profile_id                  VARCHAR(255) PRIMARY KEY,
    org_handle                   VARCHAR(255) NOT NULL,
    profile_status              VARCHAR(255),
    reference_profile_id        VARCHAR(255),
    reference_profile_org_handle VARCHAR(255),
    reference_reason            VARCHAR(255)
);

CREATE TABLE profile_schema
(
    attribute_id           VARCHAR(255) NOT NULL PRIMARY KEY,
    scope                  VARCHAR(255),
    org_handle              VARCHAR(255) NOT NULL,
    attribute_name         VARCHAR(255) NOT NULL,
    display_name           VARCHAR(255) NOT NULL ,
    value_type             VARCHAR(255) NOT NULL,
    merge_strategy         VARCHAR(255) NOT NULL,
    application_identifier VARCHAR(255) NOT NULL,
    mutability             VARCHAR(255) NOT NULL,
    multi_valued           BOOLEAN DEFAULT FALSE,
    canonical_values       JSONB   DEFAULT '[]'::jsonb,
    sub_attributes         JSONB   DEFAULT '[]'::jsonb,
    scim_dialect VARCHAR(255)
);

CREATE TABLE unification_rules
(
    rule_id       VARCHAR(255) PRIMARY KEY,
    org_handle     VARCHAR(255) NOT NULL,
    rule_name     VARCHAR(255) NOT NULL,
    property_name VARCHAR(255) NOT NULL,
    property_id   VARCHAR(255) REFERENCES profile_schema(attribute_id) ON DELETE CASCADE,
    priority      INT          NOT NULL,
    is_active     BOOLEAN      NOT NULL,
    attribute_type     VARCHAR(255) NOT NULL DEFAULT 'PRIMITIVE_EXACT',
    unification_method VARCHAR(255) NOT NULL DEFAULT 'deterministic',
    -- NULL means "derive from attribute_type"; a value is an operator override. No column
    -- default, deliberately — see docs/concepts/unification-rules.md.
    match_strength     VARCHAR(20),
    mismatch_strength  VARCHAR(20),
    created_at    TIMESTAMPTZ  NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ  NOT NULL DEFAULT now()
);

-- Application Data Table
CREATE TABLE application_data
(
    app_data_id      SERIAL PRIMARY KEY,
    profile_id       VARCHAR(255) REFERENCES profiles (profile_id) ON DELETE CASCADE,
    app_id           VARCHAR(255) NOT NULL,
    application_data JSONB DEFAULT '{}'::jsonb,
    UNIQUE (profile_id, app_id)
);

CREATE TABLE applications
(
    app_id         VARCHAR(255) PRIMARY KEY,
    org_handle     VARCHAR(255) NOT NULL,
    client_id      VARCHAR(255),
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (org_handle, client_id)
);

CREATE TABLE consent_categories
(
    id                  SERIAL PRIMARY KEY,
    org_handle           VARCHAR(255)        NOT NULL,
    category_name       VARCHAR(255)        NOT NULL,
    category_identifier VARCHAR(255) UNIQUE NOT NULL,
    purpose             VARCHAR(255)        NOT NULL,
    destinations        TEXT[],
    is_mandatory        BOOLEAN             NOT NULL DEFAULT FALSE
);

CREATE TABLE consent_category_attributes
(
    id                     SERIAL PRIMARY KEY,
    category_id            VARCHAR(255) REFERENCES consent_categories (category_identifier) ON DELETE CASCADE,
    scope                  VARCHAR(50)  NOT NULL,
    attribute_name         VARCHAR(255) NOT NULL,
    attribute_id           VARCHAR(255) REFERENCES profile_schema (attribute_id) ON DELETE CASCADE,
    application_identifier VARCHAR(255) NOT NULL DEFAULT '',
    UNIQUE (category_id, scope, attribute_name, application_identifier)
);

CREATE TABLE profile_consents
(
    id      SERIAL PRIMARY KEY,
    profile_id       VARCHAR(255) REFERENCES profiles (profile_id) ON DELETE CASCADE,
    category_id      VARCHAR (255) REFERENCES consent_categories (category_identifier) ON DELETE CASCADE,
    consent_status   BOOLEAN     NOT NULL,
    consented_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (profile_id, category_id)
);

CREATE TABLE profile_cookies (
    cookie_id VARCHAR (255) PRIMARY KEY,
    profile_id VARCHAR (255) NOT NULL REFERENCES profiles (profile_id) ON DELETE CASCADE,
    is_active BOOLEAN NOT NULL DEFAULT true
);

-- Prevents duplicate entries for the same profile and app (it generally does upsert)
ALTER TABLE application_data
    ADD CONSTRAINT unique_profile_app UNIQUE (profile_id, app_id);

-- CDS Config Table
CREATE TABLE cds_config (
    org_handle VARCHAR(255) NOT NULL,
    config VARCHAR(255) NOT NULL,
    value VARCHAR(500),
    PRIMARY KEY (org_handle, config)
);

CREATE TABLE IF NOT EXISTS organizations (
    org_id         VARCHAR(255) PRIMARY KEY,
    org_handle     VARCHAR(255) NOT NULL UNIQUE,
    org_name       VARCHAR(255),
    parent_org_id  VARCHAR(255),
    primary_org_id    VARCHAR(255) NOT NULL,
    status         VARCHAR(32)  NOT NULL,
    created_at     TIMESTAMPTZ  NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ  NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS resource_sharing_policy (
    policy_id         VARCHAR(255) PRIMARY KEY,
    resource_type     VARCHAR(64)  NOT NULL,
    resource_id       VARCHAR(255) NOT NULL,
    owning_org_id     VARCHAR(255) NOT NULL,
    initiating_org_id VARCHAR(255) NOT NULL,
    UNIQUE (resource_type, resource_id, initiating_org_id)
);

CREATE TABLE IF NOT EXISTS resource_sharing_policy_target (
    policy_id     VARCHAR(255) NOT NULL REFERENCES resource_sharing_policy (policy_id) ON DELETE CASCADE,
    target_scope  VARCHAR(32)  NOT NULL,
    target_org_id VARCHAR(255) NOT NULL,
    PRIMARY KEY (policy_id, target_scope, target_org_id)
);

-- The largest table in the system. The absent surrogate key, the primary key's column
-- order and the two partition moduli were all settled by measurement: read "How the index
-- is stored" in docs/concepts/how-unification-works.md before changing any of them.
CREATE TABLE IF NOT EXISTS blocking_keys (
    profile_id      VARCHAR(255) NOT NULL REFERENCES profiles(profile_id) ON DELETE CASCADE,
    org_handle      VARCHAR(255) NOT NULL,
    attribute_name  VARCHAR(255) NOT NULL,       -- Rule property name (e.g., "identity_attributes.emailaddress")
    key_value       VARCHAR(512) NOT NULL,       -- Normalized blocking key; 512 because it holds a whole value, not an id

    -- Column order matches the candidate lookup: equality, equality, then IN.
    PRIMARY KEY (org_handle, attribute_name, key_value, profile_id)
) PARTITION BY HASH (org_handle);

-- 8 x 4 = 32 leaves; the loop bounds are just REMAINDER 0..modulus-1. A modulus cannot be
-- changed without rebuilding the table, so read the doc before touching either.
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

-- Serves the per-profile delete during re-indexing, which has no key_value to prune on.
CREATE INDEX IF NOT EXISTS idx_blocking_keys_org_profile ON blocking_keys(org_handle, profile_id);

CREATE TABLE IF NOT EXISTS review_tasks (
    id                  VARCHAR(255) PRIMARY KEY,
    org_handle          VARCHAR(255) NOT NULL,
    incoming_profile_id   VARCHAR(255) NOT NULL REFERENCES profiles(profile_id) ON DELETE CASCADE,  -- New/edited profile that triggered the review
    candidate_profile_id   VARCHAR(255) NOT NULL REFERENCES profiles(profile_id) ON DELETE CASCADE,  -- Candidate profile that matched
    match_score         DECIMAL(5,4) NOT NULL,       -- Score between 0 and 1 (e.g., 0.87)
    status              VARCHAR(50) NOT NULL DEFAULT 'PENDING',  -- PENDING, APPROVED, CANCELLED, REJECTED
    match_reason        TEXT,                        -- JSON or text explaining why matched
    score_breakdown     JSONB DEFAULT '{}'::jsonb,   -- Detailed score per attribute
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    resolved_at         TIMESTAMPTZ,
    resolved_by         VARCHAR(255),                -- User ID who resolved
    resolution_notes    TEXT,
    CONSTRAINT uq_review_task_profiles UNIQUE (incoming_profile_id, candidate_profile_id)
);

-- Index for fetching pending reviews by org
CREATE INDEX IF NOT EXISTS idx_review_tasks_org_status ON review_tasks(org_handle, status);
CREATE INDEX IF NOT EXISTS idx_review_tasks_created ON review_tasks(created_at DESC);
CREATE INDEX IF NOT EXISTS idx_review_tasks_incoming ON review_tasks(incoming_profile_id, status);

-- Pairs an administrator explicitly rejected as non-matches. Kept separate from
-- review_tasks so profile edits and deletes can clear them without losing task history.
CREATE TABLE IF NOT EXISTS rejection_pairs (
    id              VARCHAR(255) PRIMARY KEY,
    org_handle      VARCHAR(255) NOT NULL,
    -- Lower id first, so one row is one pair whichever way the review task ran.
    profile_id_1    VARCHAR(255) NOT NULL,
    profile_id_2    VARCHAR(255) NOT NULL,
    -- The evidence the rejection was made against; reconsidered only when later evidence
    -- is materially stronger. See "Rejections" in docs/concepts/unification-rules.md.
    match_score     DECIMAL(5,4),
    score_breakdown JSONB DEFAULT '{}'::jsonb,
    rejected_by     VARCHAR(255),
    rejected_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT uq_rejection_pair UNIQUE (profile_id_1, profile_id_2)
);

CREATE INDEX IF NOT EXISTS idx_rejection_pairs_p1 ON rejection_pairs(org_handle, profile_id_1);
CREATE INDEX IF NOT EXISTS idx_rejection_pairs_p2 ON rejection_pairs(org_handle, profile_id_2);

-- ================================
-- PROFILES (Hot path: tenant + cursor pagination + ordering)
-- ================================
CREATE INDEX IF NOT EXISTS idx_profiles_org_created_profile
    ON profiles (org_handle, created_at, profile_id);

-- user_id filtering within tenant
CREATE INDEX IF NOT EXISTS idx_profiles_org_user
    ON profiles (org_handle, user_id);

-- Optional: speeds ILIKE/contains on user_id
CREATE EXTENSION IF NOT EXISTS pg_trgm;
CREATE INDEX IF NOT EXISTS idx_profiles_user_id_trgm
    ON profiles USING GIN (user_id gin_trgm_ops);

-- ================================
-- PROFILE_REFERENCE (Join + status filtering)
-- ================================
CREATE INDEX IF NOT EXISTS idx_profile_reference_status_profile
    ON profile_reference (profile_status, profile_id);

-- Optional but useful if org filtering is frequent on reference table
CREATE INDEX IF NOT EXISTS idx_profile_reference_org_status_profile
    ON profile_reference (org_handle, profile_status, profile_id);

-- For lookups by reference_profile_id
CREATE INDEX IF NOT EXISTS idx_profile_reference_reference_profile
    ON profile_reference (reference_profile_id);

-- ================================
-- APPLICATION_DATA (Joins + filtering)
-- ================================
-- Postgres does NOT auto-index FK columns → required
CREATE INDEX IF NOT EXISTS idx_application_data_profile_id
    ON application_data (profile_id);

-- Useful if querying by app_id alone
CREATE INDEX IF NOT EXISTS idx_application_data_app_id
    ON application_data (app_id);

-- JSONB filtering inside app_specific_data
CREATE INDEX IF NOT EXISTS idx_application_data_app_specific_gin
    ON application_data USING GIN ((application_data -> 'app_specific_data'));

-- ================================
-- JSONB FILTERING (Profiles)
-- ================================
-- These help mainly after switching eq → @> (still safe to add now)
CREATE INDEX IF NOT EXISTS idx_profiles_traits_gin
    ON profiles USING GIN (traits);

CREATE INDEX IF NOT EXISTS idx_profiles_identity_attributes_gin
    ON profiles USING GIN (identity_attributes);

-- ================================
-- PROFILE_SCHEMA (Rare filtering, minimal indexes)
-- ================================
CREATE INDEX IF NOT EXISTS idx_profile_schema_org_scope
    ON profile_schema (org_handle, scope);

CREATE INDEX IF NOT EXISTS idx_profile_schema_org_attr_name
    ON profile_schema (org_handle, attribute_name);

-- ================================
-- UNIFICATION_RULES
-- ================================
CREATE INDEX IF NOT EXISTS idx_unification_rules_org_active_priority
    ON unification_rules (org_handle, is_active, priority);

CREATE INDEX IF NOT EXISTS idx_unification_rules_property_id
    ON unification_rules (property_id);

-- ================================
-- ORGANIZATIONS
-- ================================
CREATE INDEX IF NOT EXISTS idx_organizations_primary
    ON organizations (primary_org_id);

-- ================================
-- RESOURCE_SHARING_POLICY
-- ================================
CREATE INDEX IF NOT EXISTS idx_resource_sharing_policy_owning_org_type
    ON resource_sharing_policy (owning_org_id, resource_type);
