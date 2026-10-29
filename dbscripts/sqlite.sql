CREATE TABLE IF NOT EXISTS profile_unification_modes
(
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    org_id     VARCHAR(255) NOT NULL,
    merge_type VARCHAR(255) NOT NULL,
    rule       VARCHAR(255) NOT NULL,
    UNIQUE (org_id, merge_type, rule)
);

CREATE TABLE IF NOT EXISTS profile_unification_triggers
(
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    org_id       VARCHAR(255) NOT NULL UNIQUE,
    trigger_type VARCHAR(255) NOT NULL,
    last_trigger BIGINT DEFAULT 0,
    duration     BIGINT DEFAULT 0
);

-- Profiles Table
CREATE TABLE IF NOT EXISTS profiles
(
    profile_id          VARCHAR(255) PRIMARY KEY,
    user_id             VARCHAR(255),
    org_id              VARCHAR(255),
    created_at          TIMESTAMP NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%f', 'now') || '+00:00'),
    updated_at          TIMESTAMP NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%f', 'now') || '+00:00'),
    location            VARCHAR(255),
    origin_country      VARCHAR(255),
    list_profile        BOOLEAN DEFAULT TRUE,
    delete_profile      BOOLEAN DEFAULT FALSE,
    traits              JSONB   DEFAULT '{}',
    identity_attributes JSONB   DEFAULT '{}'
);

CREATE TABLE IF NOT EXISTS profile_reference
(
    profile_id                   VARCHAR(255) PRIMARY KEY,
    org_id                       VARCHAR(255) NOT NULL,
    profile_status               VARCHAR(255),
    reference_profile_id         VARCHAR(255),
    reference_profile_org_id    VARCHAR(255),
    reference_reason             VARCHAR(255)
);

CREATE TABLE IF NOT EXISTS profile_schema
(
    attribute_id           VARCHAR(255) NOT NULL PRIMARY KEY,
    scope                  VARCHAR(255),
    org_id                 VARCHAR(255) NOT NULL,
    attribute_name         VARCHAR(255) NOT NULL,
    display_name           VARCHAR(255) NOT NULL,
    value_type             VARCHAR(255) NOT NULL,
    merge_strategy         VARCHAR(255) NOT NULL,
    application_identifier VARCHAR(255) NOT NULL,
    mutability             VARCHAR(255) NOT NULL,
    multi_valued           BOOLEAN DEFAULT FALSE,
    canonical_values       JSONB   DEFAULT '[]',
    sub_attributes         JSONB   DEFAULT '[]',
    scim_dialect           VARCHAR(255)
);

CREATE TABLE IF NOT EXISTS unification_rules
(
    rule_id       VARCHAR(255) PRIMARY KEY,
    org_id        VARCHAR(255) NOT NULL,
    rule_name     VARCHAR(255) NOT NULL,
    property_name VARCHAR(255) NOT NULL,
    property_id   VARCHAR(255) REFERENCES profile_schema (attribute_id) ON DELETE CASCADE,
    priority      INT          NOT NULL,
    is_active     BOOLEAN      NOT NULL,
    attribute_type     VARCHAR(255) NOT NULL DEFAULT 'PRIMITIVE_EXACT',
    unification_method VARCHAR(255) NOT NULL DEFAULT 'deterministic',
    -- NULL means "derive from attribute_type"; a value is an operator override. No column
    -- default, deliberately — see docs/concepts/unification-rules.md.
    match_strength     VARCHAR(20),
    mismatch_strength  VARCHAR(20),
    created_at    TIMESTAMP    NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%f', 'now') || '+00:00'),
    updated_at    TIMESTAMP    NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%f', 'now') || '+00:00')
);

-- Application Data Table
CREATE TABLE IF NOT EXISTS application_data
(
    app_data_id      INTEGER PRIMARY KEY AUTOINCREMENT,
    profile_id       VARCHAR(255) REFERENCES profiles (profile_id) ON DELETE CASCADE,
    app_id           VARCHAR(255) NOT NULL,
    application_data JSONB DEFAULT '{}',
    UNIQUE (profile_id, app_id)
);

CREATE TABLE IF NOT EXISTS applications
(
    app_id     VARCHAR(255) PRIMARY KEY,
    org_id     VARCHAR(255) NOT NULL,
    client_id  VARCHAR(255),
    created_at TIMESTAMP NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%f', 'now') || '+00:00'),
    updated_at TIMESTAMP NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%f', 'now') || '+00:00'),
    UNIQUE (org_id, client_id)
);

CREATE TABLE IF NOT EXISTS consent_categories
(
    id                  INTEGER PRIMARY KEY AUTOINCREMENT,
    org_id              VARCHAR(255)        NOT NULL,
    category_name       VARCHAR(255)        NOT NULL,
    category_identifier VARCHAR(255) UNIQUE NOT NULL,
    purpose             VARCHAR(255)        NOT NULL,
    destinations        TEXT,
    is_mandatory        BOOLEAN             NOT NULL DEFAULT FALSE
);

CREATE TABLE IF NOT EXISTS consent_category_attributes
(
    id                     INTEGER PRIMARY KEY AUTOINCREMENT,
    category_id            VARCHAR(255) REFERENCES consent_categories (category_identifier) ON DELETE CASCADE,
    scope                  VARCHAR(50)  NOT NULL,
    attribute_name         VARCHAR(255) NOT NULL,
    attribute_id           VARCHAR(255) REFERENCES profile_schema (attribute_id) ON DELETE CASCADE,
    application_identifier VARCHAR(255) NOT NULL DEFAULT '',
    UNIQUE (category_id, scope, attribute_name, application_identifier)
);

CREATE TABLE IF NOT EXISTS profile_consents
(
    id             INTEGER PRIMARY KEY AUTOINCREMENT,
    profile_id     VARCHAR(255) REFERENCES profiles (profile_id) ON DELETE CASCADE,
    category_id    VARCHAR(255) REFERENCES consent_categories (category_identifier) ON DELETE CASCADE,
    consent_status BOOLEAN   NOT NULL,
    consented_at   TIMESTAMP NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%f', 'now') || '+00:00'),
    UNIQUE (profile_id, category_id)
);

CREATE TABLE IF NOT EXISTS profile_cookies
(
    cookie_id  VARCHAR(255) PRIMARY KEY,
    profile_id VARCHAR(255) NOT NULL REFERENCES profiles (profile_id) ON DELETE CASCADE,
    is_active  BOOLEAN      NOT NULL DEFAULT TRUE
);

-- CDS Config Table
CREATE TABLE IF NOT EXISTS cds_config
(
    org_id     VARCHAR(255) NOT NULL,
    config     VARCHAR(255) NOT NULL,
    value      VARCHAR(500),
    PRIMARY KEY (org_id, config)
);

CREATE TABLE IF NOT EXISTS organizations
(
    org_id         VARCHAR(255) PRIMARY KEY,
    org_handle     VARCHAR(255) NOT NULL UNIQUE,
    org_name       VARCHAR(255),
    parent_org_id  VARCHAR(255),
    primary_org_id    VARCHAR(255) NOT NULL,
    status         VARCHAR(32)  NOT NULL,
    created_at     TIMESTAMP    NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%f', 'now') || '+00:00'),
    updated_at     TIMESTAMP    NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%f', 'now') || '+00:00')
);

CREATE TABLE IF NOT EXISTS resource_sharing_policy
(
    policy_id         VARCHAR(255) PRIMARY KEY,
    resource_type     VARCHAR(64)  NOT NULL,
    resource_id       VARCHAR(255) NOT NULL,
    owning_org_id     VARCHAR(255) NOT NULL,
    initiating_org_id VARCHAR(255) NOT NULL,
    UNIQUE (resource_type, resource_id, initiating_org_id)
);

CREATE TABLE IF NOT EXISTS resource_sharing_policy_target
(
    policy_id     VARCHAR(255) NOT NULL REFERENCES resource_sharing_policy (policy_id) ON DELETE CASCADE,
    target_scope  VARCHAR(32)  NOT NULL,
    target_org_id VARCHAR(255) NOT NULL,
    PRIMARY KEY (policy_id, target_scope, target_org_id)
);

-- ================================
-- PROFILES (Hot path: tenant + cursor pagination + ordering)
-- ================================
CREATE INDEX IF NOT EXISTS idx_profiles_org_id_created_profile
    ON profiles (org_id, created_at, profile_id);

-- user_id filtering within tenant
CREATE INDEX IF NOT EXISTS idx_profiles_org_id_user
    ON profiles (org_id, user_id);

-- ================================
-- PROFILE_REFERENCE (Join + status filtering)
-- ================================
CREATE INDEX IF NOT EXISTS idx_profile_reference_status_profile
    ON profile_reference (profile_status, profile_id);

-- Optional but useful if org filtering is frequent on reference table
CREATE INDEX IF NOT EXISTS idx_profile_reference_org_id_status_profile
    ON profile_reference (org_id, profile_status, profile_id);

-- For lookups by reference_profile_id
CREATE INDEX IF NOT EXISTS idx_profile_reference_reference_profile
    ON profile_reference (reference_profile_id);

-- ================================
-- APPLICATION_DATA (Joins + filtering)
-- ================================
CREATE INDEX IF NOT EXISTS idx_application_data_profile_id
    ON application_data (profile_id);

-- Useful if querying by app_id alone
CREATE INDEX IF NOT EXISTS idx_application_data_app_id
    ON application_data (app_id);

-- ================================
-- PROFILE_SCHEMA (Rare filtering, minimal indexes)
-- ================================
CREATE INDEX IF NOT EXISTS idx_profile_schema_org_id_scope
    ON profile_schema (org_id, scope);

CREATE INDEX IF NOT EXISTS idx_profile_schema_org_id_attr_name
    ON profile_schema (org_id, attribute_name);

-- ================================
-- UNIFICATION_RULES
-- ================================
CREATE INDEX IF NOT EXISTS idx_unification_rules_org_id_active_priority
    ON unification_rules (org_id, is_active, priority);

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

-- Identity Resolution: the blocking index, review queue, rejections and merge audit.

-- No surrogate key, and none of the PostgreSQL partitioning: SQLite has neither the feature
-- nor the scale. See "How the index is stored" in docs/concepts/how-unification-works.md.
CREATE TABLE IF NOT EXISTS blocking_keys
(
    profile_id     VARCHAR(255) NOT NULL REFERENCES profiles (profile_id) ON DELETE CASCADE,
    org_handle     VARCHAR(255) NOT NULL,
    attribute_name VARCHAR(255) NOT NULL, -- Rule property name (e.g. "identity_attributes.emailaddress")
    key_value      VARCHAR(512) NOT NULL, -- Normalized blocking key
    PRIMARY KEY (org_handle, attribute_name, key_value, profile_id)
);

CREATE INDEX IF NOT EXISTS idx_blocking_keys_org_profile ON blocking_keys (org_handle, profile_id);

CREATE TABLE IF NOT EXISTS review_tasks
(
    id                   VARCHAR(255) PRIMARY KEY,
    org_handle           VARCHAR(255)  NOT NULL,
    incoming_profile_id  VARCHAR(255)  NOT NULL REFERENCES profiles (profile_id) ON DELETE CASCADE,
    candidate_profile_id VARCHAR(255)  NOT NULL REFERENCES profiles (profile_id) ON DELETE CASCADE,
    match_score          DECIMAL(5, 4) NOT NULL,
    status               VARCHAR(50)   NOT NULL DEFAULT 'PENDING',
    match_reason         TEXT,
    score_breakdown      TEXT                   DEFAULT '{}',
    created_at           TIMESTAMP     NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%f', 'now') || '+00:00'),
    resolved_at          TIMESTAMP,
    resolved_by          VARCHAR(255),
    resolution_notes     TEXT,
    CONSTRAINT uq_review_task_profiles UNIQUE (incoming_profile_id, candidate_profile_id)
);

CREATE INDEX IF NOT EXISTS idx_review_tasks_org_status ON review_tasks (org_handle, status);
CREATE INDEX IF NOT EXISTS idx_review_tasks_created ON review_tasks (created_at DESC);
CREATE INDEX IF NOT EXISTS idx_review_tasks_incoming ON review_tasks (incoming_profile_id, status);

CREATE TABLE IF NOT EXISTS rejection_pairs
(
    id           VARCHAR(255) PRIMARY KEY,
    org_handle   VARCHAR(255) NOT NULL,
    -- Lower id first, so one row is one pair whichever way the review task ran.
    profile_id_1 VARCHAR(255) NOT NULL,
    profile_id_2 VARCHAR(255) NOT NULL,
    -- The evidence the decision was made against; see the PostgreSQL schema for why.
    match_score     DECIMAL(5, 4),
    score_breakdown TEXT DEFAULT '{}',
    rejected_by  VARCHAR(255),
    rejected_at  TIMESTAMP    NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%f', 'now') || '+00:00'),
    CONSTRAINT uq_rejection_pair UNIQUE (profile_id_1, profile_id_2)
);

CREATE INDEX IF NOT EXISTS idx_rejection_pairs_p1 ON rejection_pairs (org_handle, profile_id_1);
CREATE INDEX IF NOT EXISTS idx_rejection_pairs_p2 ON rejection_pairs (org_handle, profile_id_2);

