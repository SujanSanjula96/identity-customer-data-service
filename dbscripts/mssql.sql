IF OBJECT_ID(N'dbo.profiles', N'U') IS NULL
BEGIN
CREATE TABLE dbo.profiles
(
    profile_id          NVARCHAR(36) COLLATE Latin1_General_100_BIN2 NOT NULL PRIMARY KEY,
    user_id             NVARCHAR(255) COLLATE Latin1_General_100_BIN2,
    created_at          DATETIME2(6) NOT NULL DEFAULT SYSUTCDATETIME(),
    updated_at          DATETIME2(6) NOT NULL DEFAULT SYSUTCDATETIME(),
    location            NVARCHAR(255) COLLATE Latin1_General_100_BIN2,
    origin_country      NVARCHAR(255) COLLATE Latin1_General_100_BIN2,
    list_profile        BIT DEFAULT 1,
    delete_profile      BIT DEFAULT 0,
    traits              NVARCHAR(MAX) COLLATE Latin1_General_100_BIN2 DEFAULT '{}' CHECK (ISJSON(traits) = 1 OR traits = N'null'),
    identity_attributes NVARCHAR(MAX) COLLATE Latin1_General_100_BIN2 DEFAULT '{}' CHECK (ISJSON(identity_attributes) = 1 OR identity_attributes = N'null'),
    org_id              NVARCHAR(255) COLLATE Latin1_General_100_BIN2 NOT NULL
);
CREATE INDEX idx_profiles_org_id_created_profile ON dbo.profiles (org_id, created_at, profile_id);
CREATE INDEX idx_profiles_org_id_user ON dbo.profiles (org_id, user_id);
END;

IF OBJECT_ID(N'dbo.profile_reference', N'U') IS NULL
BEGIN
CREATE TABLE dbo.profile_reference
(
    profile_id               NVARCHAR(36) COLLATE Latin1_General_100_BIN2 NOT NULL PRIMARY KEY,
    profile_status           NVARCHAR(255) COLLATE Latin1_General_100_BIN2,
    reference_profile_id     NVARCHAR(36) COLLATE Latin1_General_100_BIN2,
    reference_reason         NVARCHAR(255) COLLATE Latin1_General_100_BIN2,
    org_id                   NVARCHAR(255) COLLATE Latin1_General_100_BIN2 NOT NULL,
    reference_profile_org_id NVARCHAR(255) COLLATE Latin1_General_100_BIN2
);
CREATE INDEX idx_profile_reference_status_profile ON dbo.profile_reference (profile_status, profile_id);
CREATE INDEX idx_profile_reference_org_id_status_profile ON dbo.profile_reference (org_id, profile_status, profile_id);
CREATE INDEX idx_profile_reference_reference_profile ON dbo.profile_reference (reference_profile_id);
END;

IF OBJECT_ID(N'dbo.profile_schema', N'U') IS NULL
BEGIN
CREATE TABLE dbo.profile_schema
(
    attribute_id           NVARCHAR(255) COLLATE Latin1_General_100_BIN2 NOT NULL PRIMARY KEY,
    scope                  NVARCHAR(255) COLLATE Latin1_General_100_BIN2,
    attribute_name         NVARCHAR(255) COLLATE Latin1_General_100_BIN2 NOT NULL,
    display_name           NVARCHAR(255) COLLATE Latin1_General_100_BIN2 NOT NULL,
    value_type             NVARCHAR(255) COLLATE Latin1_General_100_BIN2 NOT NULL,
    merge_strategy         NVARCHAR(255) COLLATE Latin1_General_100_BIN2 NOT NULL,
    application_identifier NVARCHAR(255) COLLATE Latin1_General_100_BIN2 NOT NULL,
    mutability             NVARCHAR(255) COLLATE Latin1_General_100_BIN2 NOT NULL,
    multi_valued           BIT DEFAULT 0,
    canonical_values       NVARCHAR(MAX) COLLATE Latin1_General_100_BIN2 DEFAULT '[]',
    sub_attributes         NVARCHAR(MAX) COLLATE Latin1_General_100_BIN2 DEFAULT '[]',
    scim_dialect           NVARCHAR(255) COLLATE Latin1_General_100_BIN2,
    org_id                 NVARCHAR(255) COLLATE Latin1_General_100_BIN2 NOT NULL
);
CREATE INDEX idx_profile_schema_org_id_scope ON dbo.profile_schema (org_id, scope);
CREATE INDEX idx_profile_schema_org_id_attr_name ON dbo.profile_schema (org_id, attribute_name);
END;

IF OBJECT_ID(N'dbo.unification_rules', N'U') IS NULL
BEGIN
CREATE TABLE dbo.unification_rules
(
    rule_id            NVARCHAR(255) COLLATE Latin1_General_100_BIN2 NOT NULL PRIMARY KEY,
    rule_name          NVARCHAR(255) COLLATE Latin1_General_100_BIN2 NOT NULL,
    property_name      NVARCHAR(255) COLLATE Latin1_General_100_BIN2 NOT NULL,
    property_id        NVARCHAR(255) COLLATE Latin1_General_100_BIN2 REFERENCES dbo.profile_schema (attribute_id) ON DELETE CASCADE,
    priority           INT NOT NULL,
    is_active          BIT NOT NULL,
    attribute_type     NVARCHAR(255) COLLATE Latin1_General_100_BIN2 NOT NULL DEFAULT 'PRIMITIVE_EXACT',
    unification_method NVARCHAR(255) COLLATE Latin1_General_100_BIN2 NOT NULL DEFAULT 'deterministic',
    -- NULL means "derive from attribute_type"; a value is an operator override. No column
    -- default, deliberately: see docs/concepts/unification-rules.md.
    match_strength     NVARCHAR(20) COLLATE Latin1_General_100_BIN2,
    mismatch_strength  NVARCHAR(20) COLLATE Latin1_General_100_BIN2,
    created_at         DATETIME2(6) NOT NULL DEFAULT SYSUTCDATETIME(),
    updated_at         DATETIME2(6) NOT NULL DEFAULT SYSUTCDATETIME(),
    org_id             NVARCHAR(255) COLLATE Latin1_General_100_BIN2 NOT NULL
);
CREATE INDEX idx_unification_rules_org_id_active_priority ON dbo.unification_rules (org_id, is_active, priority);
CREATE INDEX idx_unification_rules_property_id ON dbo.unification_rules (property_id);
END;

IF OBJECT_ID(N'dbo.application_data', N'U') IS NULL
BEGIN
CREATE TABLE dbo.application_data
(
    app_data_id      INT IDENTITY(1,1) PRIMARY KEY,
    profile_id       NVARCHAR(36) COLLATE Latin1_General_100_BIN2 REFERENCES dbo.profiles (profile_id) ON DELETE CASCADE,
    app_id           NVARCHAR(255) COLLATE Latin1_General_100_BIN2 NOT NULL,
    application_data NVARCHAR(MAX) COLLATE Latin1_General_100_BIN2 DEFAULT '{}' CHECK (ISJSON(application_data) = 1 OR application_data = N'null')
);
CREATE UNIQUE INDEX unique_profile_app ON dbo.application_data (profile_id, app_id) WHERE profile_id IS NOT NULL;
CREATE INDEX idx_application_data_app_id ON dbo.application_data (app_id);
END;

IF OBJECT_ID(N'dbo.applications', N'U') IS NULL
BEGIN
CREATE TABLE dbo.applications
(
    app_id     NVARCHAR(255) COLLATE Latin1_General_100_BIN2 NOT NULL PRIMARY KEY,
    client_id  NVARCHAR(255) COLLATE Latin1_General_100_BIN2,
    created_at DATETIME2(6) NOT NULL DEFAULT SYSUTCDATETIME(),
    updated_at DATETIME2(6) NOT NULL DEFAULT SYSUTCDATETIME(),
    org_id     NVARCHAR(255) COLLATE Latin1_General_100_BIN2 NOT NULL
);
CREATE UNIQUE INDEX uq_applications_org_client ON dbo.applications (org_id, client_id) WHERE client_id IS NOT NULL;
END;

IF OBJECT_ID(N'dbo.consent_categories', N'U') IS NULL
BEGIN
CREATE TABLE dbo.consent_categories
(
    id                  INT IDENTITY(1,1) PRIMARY KEY,
    category_name       NVARCHAR(255) COLLATE Latin1_General_100_BIN2 NOT NULL,
    category_identifier NVARCHAR(36) COLLATE Latin1_General_100_BIN2 NOT NULL UNIQUE,
    purpose             NVARCHAR(255) COLLATE Latin1_General_100_BIN2 NOT NULL,
    destinations        NVARCHAR(MAX) COLLATE Latin1_General_100_BIN2,
    is_mandatory        BIT NOT NULL DEFAULT 0,
    org_id              NVARCHAR(255) COLLATE Latin1_General_100_BIN2 NOT NULL
);
END;

IF OBJECT_ID(N'dbo.consent_category_attributes', N'U') IS NULL
BEGIN
CREATE TABLE dbo.consent_category_attributes
(
    id                     INT IDENTITY(1,1) PRIMARY KEY,
    category_id            NVARCHAR(36) COLLATE Latin1_General_100_BIN2 REFERENCES dbo.consent_categories (category_identifier) ON DELETE CASCADE,
    scope                  NVARCHAR(50) COLLATE Latin1_General_100_BIN2 NOT NULL,
    attribute_name         NVARCHAR(255) COLLATE Latin1_General_100_BIN2 NOT NULL,
    attribute_id           NVARCHAR(255) COLLATE Latin1_General_100_BIN2 REFERENCES dbo.profile_schema (attribute_id) ON DELETE CASCADE,
    application_identifier NVARCHAR(255) COLLATE Latin1_General_100_BIN2 NOT NULL DEFAULT '',
    CONSTRAINT uq_consent_category_attributes UNIQUE (category_id, scope, attribute_name, application_identifier)
);
END;

IF OBJECT_ID(N'dbo.profile_consents', N'U') IS NULL
BEGIN
CREATE TABLE dbo.profile_consents
(
    id             INT IDENTITY(1,1) PRIMARY KEY,
    profile_id     NVARCHAR(36) COLLATE Latin1_General_100_BIN2 REFERENCES dbo.profiles (profile_id) ON DELETE CASCADE,
    category_id    NVARCHAR(36) COLLATE Latin1_General_100_BIN2 REFERENCES dbo.consent_categories (category_identifier) ON DELETE CASCADE,
    consent_status BIT NOT NULL,
    consented_at   DATETIME2(6) NOT NULL DEFAULT SYSUTCDATETIME(),
    CONSTRAINT uq_profile_consents UNIQUE (profile_id, category_id)
);
END;

IF OBJECT_ID(N'dbo.profile_cookies', N'U') IS NULL
BEGIN
CREATE TABLE dbo.profile_cookies
(
    cookie_id  NVARCHAR(255) COLLATE Latin1_General_100_BIN2 NOT NULL PRIMARY KEY,
    profile_id NVARCHAR(36) COLLATE Latin1_General_100_BIN2 NOT NULL REFERENCES dbo.profiles (profile_id) ON DELETE CASCADE,
    is_active  BIT NOT NULL DEFAULT 1
);
END;

IF OBJECT_ID(N'dbo.cds_config', N'U') IS NULL
BEGIN
CREATE TABLE dbo.cds_config
(
    config NVARCHAR(255) COLLATE Latin1_General_100_BIN2 NOT NULL,
    value  NVARCHAR(500) COLLATE Latin1_General_100_BIN2,
    org_id NVARCHAR(255) COLLATE Latin1_General_100_BIN2 NOT NULL,
    CONSTRAINT pk_cds_config PRIMARY KEY NONCLUSTERED (org_id, config)
);
END;

-- Credentials of the Identity Server application CDS calls the management APIs
-- with, one row per organization, sent by the Identity Server when the
-- organization enables CDS.
--
-- client_secret is sealed, never plaintext, and key_id names the key that sealed
-- it. It cannot be hashed: CDS presents this secret to the token endpoint on every
-- token request, and a hash cannot be presented.
IF OBJECT_ID(N'dbo.org_service_credentials', N'U') IS NULL
BEGIN
CREATE TABLE dbo.org_service_credentials
(
    org_id        NVARCHAR(255) COLLATE Latin1_General_100_BIN2 NOT NULL PRIMARY KEY,
    client_id     NVARCHAR(255) COLLATE Latin1_General_100_BIN2 NOT NULL,
    client_secret NVARCHAR(MAX) COLLATE Latin1_General_100_BIN2 NOT NULL,
    key_id        NVARCHAR(64) COLLATE Latin1_General_100_BIN2 NOT NULL
);
END;

IF OBJECT_ID(N'dbo.organizations', N'U') IS NULL
BEGIN
CREATE TABLE dbo.organizations
(
    org_id         NVARCHAR(255) COLLATE Latin1_General_100_BIN2 NOT NULL PRIMARY KEY,
    org_handle     NVARCHAR(255) COLLATE Latin1_General_100_BIN2 NOT NULL UNIQUE,
    org_name       NVARCHAR(255) COLLATE Latin1_General_100_BIN2,
    parent_org_id  NVARCHAR(255) COLLATE Latin1_General_100_BIN2,
    primary_org_id NVARCHAR(255) COLLATE Latin1_General_100_BIN2 NOT NULL,
    status         NVARCHAR(32) COLLATE Latin1_General_100_BIN2 NOT NULL,
    created_at     DATETIME2(6) NOT NULL DEFAULT SYSUTCDATETIME(),
    updated_at     DATETIME2(6) NOT NULL DEFAULT SYSUTCDATETIME()
);
CREATE INDEX idx_organizations_primary ON dbo.organizations (primary_org_id);
END;

IF OBJECT_ID(N'dbo.resource_sharing_policy', N'U') IS NULL
BEGIN
CREATE TABLE dbo.resource_sharing_policy
(
    policy_id         NVARCHAR(255) COLLATE Latin1_General_100_BIN2 NOT NULL PRIMARY KEY,
    resource_type     NVARCHAR(64) COLLATE Latin1_General_100_BIN2 NOT NULL,
    resource_id       NVARCHAR(255) COLLATE Latin1_General_100_BIN2 NOT NULL,
    owning_org_id     NVARCHAR(255) COLLATE Latin1_General_100_BIN2 NOT NULL,
    initiating_org_id NVARCHAR(255) COLLATE Latin1_General_100_BIN2 NOT NULL,
    CONSTRAINT uq_resource_sharing_policy UNIQUE (resource_type, resource_id, initiating_org_id)
);
CREATE INDEX idx_resource_sharing_policy_owning_org_type ON dbo.resource_sharing_policy (owning_org_id, resource_type);
END;

IF OBJECT_ID(N'dbo.resource_sharing_policy_target', N'U') IS NULL
BEGIN
CREATE TABLE dbo.resource_sharing_policy_target
(
    policy_id     NVARCHAR(255) COLLATE Latin1_General_100_BIN2 NOT NULL REFERENCES dbo.resource_sharing_policy (policy_id) ON DELETE CASCADE,
    target_scope  NVARCHAR(32) COLLATE Latin1_General_100_BIN2 NOT NULL,
    target_org_id NVARCHAR(255) COLLATE Latin1_General_100_BIN2 NOT NULL,
    CONSTRAINT pk_resource_sharing_policy_target PRIMARY KEY NONCLUSTERED (policy_id, target_scope, target_org_id)
);
END;

-- The largest table in the system. The absent surrogate key and the primary key's
-- column order were settled by measurement: read "How the index is stored" in
-- docs/concepts/how-unification-works.md before changing either.
IF OBJECT_ID(N'dbo.blocking_keys', N'U') IS NULL
BEGIN
CREATE TABLE dbo.blocking_keys
(
    profile_id     NVARCHAR(36) COLLATE Latin1_General_100_BIN2 NOT NULL REFERENCES dbo.profiles (profile_id) ON DELETE CASCADE,
    attribute_name NVARCHAR(128) COLLATE Latin1_General_100_BIN2 NOT NULL, -- Rule property name (e.g., "identity_attributes.emailaddress")
    key_value      NVARCHAR(512) COLLATE Latin1_General_100_BIN2 NOT NULL, -- Normalized blocking key; 512 because it holds a whole value, not an id
    org_id         NVARCHAR(36) COLLATE Latin1_General_100_BIN2 NOT NULL,
    -- Column order matches the candidate lookup: equality, equality, then IN.
    CONSTRAINT pk_blocking_keys PRIMARY KEY NONCLUSTERED (org_id, attribute_name, key_value, profile_id)
);
-- Serves the per-profile delete during re-indexing, which has no key_value to prune on.
CREATE INDEX idx_blocking_keys_org_id_profile ON dbo.blocking_keys (org_id, profile_id);
END;

IF OBJECT_ID(N'dbo.review_tasks', N'U') IS NULL
BEGIN
CREATE TABLE dbo.review_tasks
(
    id                   NVARCHAR(255) COLLATE Latin1_General_100_BIN2 NOT NULL PRIMARY KEY,
    incoming_profile_id  NVARCHAR(36) COLLATE Latin1_General_100_BIN2 NOT NULL REFERENCES dbo.profiles (profile_id) ON DELETE CASCADE, -- New/edited profile that triggered the review
    candidate_profile_id NVARCHAR(36) COLLATE Latin1_General_100_BIN2 NOT NULL REFERENCES dbo.profiles (profile_id), -- Candidate profile that matched; deleted by the profile delete statement
    match_score          DECIMAL(5,4) NOT NULL, -- Score between 0 and 1 (e.g., 0.87)
    status               NVARCHAR(50) COLLATE Latin1_General_100_BIN2 NOT NULL DEFAULT 'PENDING', -- PENDING, APPROVED, CANCELLED, REJECTED
    match_reason         NVARCHAR(MAX) COLLATE Latin1_General_100_BIN2, -- JSON or text explaining why matched
    score_breakdown      NVARCHAR(MAX) COLLATE Latin1_General_100_BIN2 DEFAULT '{}', -- Detailed score per attribute
    created_at           DATETIME2(6) NOT NULL DEFAULT SYSUTCDATETIME(),
    resolved_at          DATETIME2(6),
    resolved_by          NVARCHAR(255) COLLATE Latin1_General_100_BIN2, -- User ID who resolved
    resolution_notes     NVARCHAR(MAX) COLLATE Latin1_General_100_BIN2,
    org_id               NVARCHAR(36) COLLATE Latin1_General_100_BIN2 NOT NULL,
    CONSTRAINT uq_review_task_profiles UNIQUE (incoming_profile_id, candidate_profile_id)
);
CREATE INDEX idx_review_tasks_org_id_status ON dbo.review_tasks (org_id, status);
CREATE INDEX idx_review_tasks_created ON dbo.review_tasks (created_at DESC);
CREATE INDEX idx_review_tasks_incoming ON dbo.review_tasks (incoming_profile_id, status);
CREATE INDEX idx_review_tasks_candidate ON dbo.review_tasks (candidate_profile_id, status);
END;

-- Pairs an administrator explicitly rejected as non-matches. Kept separate from
-- review_tasks so profile edits and deletes can clear them without losing task history.
IF OBJECT_ID(N'dbo.rejection_pairs', N'U') IS NULL
BEGIN
CREATE TABLE dbo.rejection_pairs
(
    id              NVARCHAR(255) COLLATE Latin1_General_100_BIN2 NOT NULL PRIMARY KEY,
    -- Lower id first, so one row is one pair whichever way the review task ran.
    profile_id_1    NVARCHAR(36) COLLATE Latin1_General_100_BIN2 NOT NULL,
    profile_id_2    NVARCHAR(36) COLLATE Latin1_General_100_BIN2 NOT NULL,
    -- The evidence the rejection was made against; reconsidered only when later evidence
    -- is materially stronger. See "Rejections" in docs/concepts/unification-rules.md.
    match_score     DECIMAL(5,4),
    score_breakdown NVARCHAR(MAX) COLLATE Latin1_General_100_BIN2 DEFAULT '{}',
    rejected_by     NVARCHAR(255) COLLATE Latin1_General_100_BIN2,
    rejected_at     DATETIME2(6) NOT NULL DEFAULT SYSUTCDATETIME(),
    org_id          NVARCHAR(255) COLLATE Latin1_General_100_BIN2 NOT NULL,
    CONSTRAINT uq_rejection_pair UNIQUE (profile_id_1, profile_id_2)
);
CREATE INDEX idx_rejection_pairs_org_id_p1 ON dbo.rejection_pairs (org_id, profile_id_1);
CREATE INDEX idx_rejection_pairs_org_id_p2 ON dbo.rejection_pairs (org_id, profile_id_2);
END;
