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
-- B2B: ORGANIZATIONS AND SHARING
-- ================================

-- The org tree that CDS learns from the identity provider.
CREATE TABLE organizations (
    org_id         VARCHAR(255) PRIMARY KEY,
    org_handle     VARCHAR(255) NOT NULL UNIQUE,
    org_name       VARCHAR(255),
    parent_org_id  VARCHAR(255),
    root_org_id    VARCHAR(255) NOT NULL,
    path           VARCHAR(2048) NOT NULL,
    depth          INT          NOT NULL DEFAULT 0,
    status         VARCHAR(32)  NOT NULL DEFAULT 'ACTIVE',
    created_at     TIMESTAMPTZ  NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ  NOT NULL DEFAULT now(),
    last_synced_at TIMESTAMPTZ
);

CREATE INDEX idx_organizations_root_path ON organizations (root_org_id, path);
CREATE INDEX idx_organizations_parent ON organizations (parent_org_id);

-- One share policy for each resource and initiating org.
CREATE TABLE cds_share_policy (
    policy_id         VARCHAR(255) PRIMARY KEY,
    resource_type     VARCHAR(64)  NOT NULL,
    resource_id       VARCHAR(255) NOT NULL,
    owner_org_id      VARCHAR(255) NOT NULL,
    initiating_org_id VARCHAR(255) NOT NULL,
    stage             VARCHAR(16)  NOT NULL,
    parent_policy_id  VARCHAR(255) REFERENCES cds_share_policy (policy_id) ON DELETE CASCADE,
    version           INT          NOT NULL DEFAULT 1,
    created_at        TIMESTAMPTZ  NOT NULL DEFAULT now(),
    updated_at        TIMESTAMPTZ  NOT NULL DEFAULT now(),
    UNIQUE (resource_type, resource_id, initiating_org_id)
);

CREATE TABLE cds_share_policy_target (
    policy_id     VARCHAR(255) NOT NULL REFERENCES cds_share_policy (policy_id) ON DELETE CASCADE,
    target_scope  VARCHAR(32)  NOT NULL,
    target_org_id VARCHAR(255) NOT NULL DEFAULT '',
    PRIMARY KEY (policy_id, target_scope, target_org_id)
);

CREATE TABLE cds_share_policy_exclusion (
    policy_id       VARCHAR(255) NOT NULL REFERENCES cds_share_policy (policy_id) ON DELETE CASCADE,
    excluded_org_id VARCHAR(255) NOT NULL,
    PRIMARY KEY (policy_id, excluded_org_id)
);

-- The result of the policies for each resource and org.
CREATE TABLE cds_share_state (
    resource_type           VARCHAR(64)  NOT NULL,
    resource_id             VARCHAR(255) NOT NULL,
    org_id                  VARCHAR(255) NOT NULL,
    root_org_id             VARCHAR(255) NOT NULL,
    state                   VARCHAR(64)  NOT NULL,
    reason                  VARCHAR(500),
    conflicting_resource_id VARCHAR(255),
    updated_at              TIMESTAMPTZ  NOT NULL DEFAULT now(),
    PRIMARY KEY (resource_type, resource_id, org_id)
);

CREATE INDEX idx_share_state_org ON cds_share_state (org_id, resource_type, state);
