-- Copyright (c) 2026, Choreo Labs LLC. (https://choreolabs.com).
--
-- Choreo Labs licenses this file to you under the Apache License,
-- Version 2.0 (the "License"); you may not use this file except
-- in compliance with the License.
-- You may obtain a copy of the License at
--
-- http://www.apache.org/licenses/LICENSE-2.0
--
-- Unless required by applicable law or agreed to in writing,
-- software distributed under the License is distributed on an
-- "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY
-- KIND, either express or implied.  See the License for the
-- specific language governing permissions and limitations
-- under the License.

CREATE TABLE IF NOT EXISTS profiles
(
    profile_id          VARCHAR(36)  NOT NULL PRIMARY KEY,
    user_id             VARCHAR(255),
    created_at          DATETIME(6)  NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at          DATETIME(6)  NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    location            VARCHAR(255),
    origin_country      VARCHAR(255),
    list_profile        BOOLEAN DEFAULT TRUE,
    delete_profile      BOOLEAN DEFAULT FALSE,
    traits              JSON DEFAULT (JSON_OBJECT()),
    identity_attributes JSON DEFAULT (JSON_OBJECT()),
    org_id              VARCHAR(255) NOT NULL,
    INDEX idx_profiles_org_id_created_profile (org_id, created_at, profile_id),
    INDEX idx_profiles_org_id_user (org_id, user_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;

CREATE TABLE IF NOT EXISTS profile_reference
(
    profile_id               VARCHAR(36) NOT NULL PRIMARY KEY,
    profile_status           VARCHAR(255),
    reference_profile_id     VARCHAR(36),
    reference_reason         VARCHAR(255),
    org_id                   VARCHAR(255) NOT NULL,
    reference_profile_org_id VARCHAR(255),
    INDEX idx_profile_reference_status_profile (profile_status, profile_id),
    INDEX idx_profile_reference_org_id_status_profile (org_id, profile_status, profile_id),
    INDEX idx_profile_reference_reference_profile (reference_profile_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;

CREATE TABLE IF NOT EXISTS profile_schema
(
    attribute_id           VARCHAR(255) NOT NULL PRIMARY KEY,
    scope                  VARCHAR(255),
    attribute_name         VARCHAR(255) NOT NULL,
    display_name           VARCHAR(255) NOT NULL,
    value_type             VARCHAR(255) NOT NULL,
    merge_strategy         VARCHAR(255) NOT NULL,
    application_identifier VARCHAR(255) NOT NULL,
    mutability             VARCHAR(255) NOT NULL,
    multi_valued           BOOLEAN DEFAULT FALSE,
    canonical_values       LONGTEXT DEFAULT ('[]'),
    sub_attributes         LONGTEXT DEFAULT ('[]'),
    scim_dialect           VARCHAR(255),
    org_id                 VARCHAR(255) NOT NULL,
    INDEX idx_profile_schema_org_id_scope (org_id, scope),
    INDEX idx_profile_schema_org_id_attr_name (org_id, attribute_name)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;

CREATE TABLE IF NOT EXISTS unification_rules
(
    rule_id            VARCHAR(255) NOT NULL PRIMARY KEY,
    rule_name          VARCHAR(255) NOT NULL,
    property_name      VARCHAR(255) NOT NULL,
    property_id        VARCHAR(255),
    priority           INT          NOT NULL,
    is_active          BOOLEAN      NOT NULL,
    attribute_type     VARCHAR(255) NOT NULL DEFAULT 'PRIMITIVE_EXACT',
    unification_method VARCHAR(255) NOT NULL DEFAULT 'deterministic',
    -- NULL means "derive from attribute_type"; a value is an operator override. No column
    -- default, deliberately — see docs/concepts/unification-rules.md.
    match_strength     VARCHAR(20),
    mismatch_strength  VARCHAR(20),
    created_at         DATETIME(6)  NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at         DATETIME(6)  NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    org_id             VARCHAR(255) NOT NULL,
    CONSTRAINT fk_unification_rules_property
        FOREIGN KEY (property_id) REFERENCES profile_schema (attribute_id) ON DELETE CASCADE,
    INDEX idx_unification_rules_org_id_active_priority (org_id, is_active, priority),
    INDEX idx_unification_rules_property_id (property_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;

CREATE TABLE IF NOT EXISTS application_data
(
    app_data_id      INT AUTO_INCREMENT PRIMARY KEY,
    profile_id       VARCHAR(36),
    app_id           VARCHAR(255) NOT NULL,
    application_data JSON DEFAULT (JSON_OBJECT()),
    CONSTRAINT fk_application_data_profile
        FOREIGN KEY (profile_id) REFERENCES profiles (profile_id) ON DELETE CASCADE,
    UNIQUE KEY unique_profile_app (profile_id, app_id),
    INDEX idx_application_data_app_id (app_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;

CREATE TABLE IF NOT EXISTS applications
(
    app_id     VARCHAR(255) NOT NULL PRIMARY KEY,
    client_id  VARCHAR(255),
    created_at DATETIME(6)  NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6)  NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    org_id     VARCHAR(255) NOT NULL,
    UNIQUE KEY uq_applications_org_client (org_id, client_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;

CREATE TABLE IF NOT EXISTS consent_categories
(
    id                  INT AUTO_INCREMENT PRIMARY KEY,
    category_name       VARCHAR(255) NOT NULL,
    category_identifier VARCHAR(36)  NOT NULL,
    purpose             VARCHAR(255) NOT NULL,
    destinations        TEXT,
    is_mandatory        BOOLEAN      NOT NULL DEFAULT FALSE,
    org_id              VARCHAR(255) NOT NULL,
    UNIQUE KEY uq_consent_categories_identifier (category_identifier)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;

CREATE TABLE IF NOT EXISTS consent_category_attributes
(
    id                     INT AUTO_INCREMENT PRIMARY KEY,
    category_id            VARCHAR(36),
    scope                  VARCHAR(50)  NOT NULL,
    attribute_name         VARCHAR(255) NOT NULL,
    attribute_id           VARCHAR(255),
    application_identifier VARCHAR(255) NOT NULL DEFAULT '',
    CONSTRAINT fk_consent_attributes_category
        FOREIGN KEY (category_id) REFERENCES consent_categories (category_identifier) ON DELETE CASCADE,
    CONSTRAINT fk_consent_attributes_schema
        FOREIGN KEY (attribute_id) REFERENCES profile_schema (attribute_id) ON DELETE CASCADE,
    UNIQUE KEY uq_consent_category_attributes (category_id, scope, attribute_name, application_identifier)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;

CREATE TABLE IF NOT EXISTS profile_consents
(
    id             INT AUTO_INCREMENT PRIMARY KEY,
    profile_id     VARCHAR(36),
    category_id    VARCHAR(36),
    consent_status BOOLEAN     NOT NULL,
    consented_at   DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    CONSTRAINT fk_profile_consents_profile
        FOREIGN KEY (profile_id) REFERENCES profiles (profile_id) ON DELETE CASCADE,
    CONSTRAINT fk_profile_consents_category
        FOREIGN KEY (category_id) REFERENCES consent_categories (category_identifier) ON DELETE CASCADE,
    UNIQUE KEY uq_profile_consents (profile_id, category_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;

CREATE TABLE IF NOT EXISTS profile_cookies
(
    cookie_id  VARCHAR(255) NOT NULL PRIMARY KEY,
    profile_id VARCHAR(36)  NOT NULL,
    is_active  BOOLEAN      NOT NULL DEFAULT TRUE,
    CONSTRAINT fk_profile_cookies_profile
        FOREIGN KEY (profile_id) REFERENCES profiles (profile_id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;

-- CDS Config Table
CREATE TABLE IF NOT EXISTS cds_config
(
    config VARCHAR(255) NOT NULL,
    value  VARCHAR(500),
    org_id VARCHAR(255) NOT NULL,
    PRIMARY KEY (org_id, config)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;

-- Credentials of the Identity Server application CDS calls the management APIs
-- with, one row per organization, sent by the Identity Server when the
-- organization enables CDS.
--
-- client_secret is sealed, never plaintext, and key_id names the key that sealed
-- it. It cannot be hashed: CDS presents this secret to the token endpoint on every
-- token request, and a hash cannot be presented.
CREATE TABLE IF NOT EXISTS org_service_credentials
(
    org_id        VARCHAR(255) NOT NULL,
    client_id     VARCHAR(255) NOT NULL,
    client_secret TEXT         NOT NULL,
    key_id        VARCHAR(64)  NOT NULL,
    PRIMARY KEY (org_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;

CREATE TABLE IF NOT EXISTS organizations
(
    org_id         VARCHAR(255) NOT NULL PRIMARY KEY,
    org_handle     VARCHAR(255) NOT NULL,
    org_name       VARCHAR(255),
    parent_org_id  VARCHAR(255),
    primary_org_id VARCHAR(255) NOT NULL,
    status         VARCHAR(32)  NOT NULL,
    created_at     DATETIME(6)  NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at     DATETIME(6)  NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    UNIQUE KEY uq_organizations_handle (org_handle),
    INDEX idx_organizations_primary (primary_org_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;

CREATE TABLE IF NOT EXISTS resource_sharing_policy
(
    policy_id         VARCHAR(255) NOT NULL PRIMARY KEY,
    resource_type     VARCHAR(64)  NOT NULL,
    resource_id       VARCHAR(255) NOT NULL,
    owning_org_id     VARCHAR(255) NOT NULL,
    initiating_org_id VARCHAR(255) NOT NULL,
    UNIQUE KEY uq_resource_sharing_policy (resource_type, resource_id, initiating_org_id),
    INDEX idx_resource_sharing_policy_owning_org_type (owning_org_id, resource_type)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;

CREATE TABLE IF NOT EXISTS resource_sharing_policy_target
(
    policy_id     VARCHAR(255) NOT NULL,
    target_scope  VARCHAR(32)  NOT NULL,
    target_org_id VARCHAR(255) NOT NULL,
    PRIMARY KEY (policy_id, target_scope, target_org_id),
    CONSTRAINT fk_sharing_target_policy
        FOREIGN KEY (policy_id) REFERENCES resource_sharing_policy (policy_id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;

-- The largest table in the system. The absent surrogate key and the primary key's
-- column order were settled by measurement: read "How the index is stored" in
-- docs/concepts/how-unification-works.md before changing either.
CREATE TABLE IF NOT EXISTS blocking_keys
(
    profile_id     VARCHAR(36)  NOT NULL,
    attribute_name VARCHAR(128) NOT NULL, -- Rule property name (e.g., "identity_attributes.emailaddress")
    key_value      VARCHAR(512) NOT NULL, -- Normalized blocking key; 512 because it holds a whole value, not an id
    org_id         VARCHAR(36)  NOT NULL,
    -- Column order matches the candidate lookup: equality, equality, then IN.
    PRIMARY KEY (org_id, attribute_name, key_value, profile_id),
    CONSTRAINT fk_blocking_keys_profile
        FOREIGN KEY (profile_id) REFERENCES profiles (profile_id) ON DELETE CASCADE,
    -- Serves the per-profile delete during re-indexing, which has no key_value to prune on.
    INDEX idx_blocking_keys_org_id_profile (org_id, profile_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;

CREATE TABLE IF NOT EXISTS review_tasks
(
    id                   VARCHAR(255) NOT NULL PRIMARY KEY,
    incoming_profile_id  VARCHAR(36)  NOT NULL, -- New/edited profile that triggered the review
    candidate_profile_id VARCHAR(36)  NOT NULL, -- Candidate profile that matched
    match_score          DECIMAL(5,4) NOT NULL, -- Score between 0 and 1 (e.g., 0.87)
    status               VARCHAR(50)  NOT NULL DEFAULT 'PENDING', -- PENDING, APPROVED, CANCELLED, REJECTED
    match_reason         TEXT,                  -- JSON or text explaining why matched
    score_breakdown      LONGTEXT DEFAULT ('{}'), -- Detailed score per attribute
    created_at           DATETIME(6)  NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    resolved_at          DATETIME(6),
    resolved_by          VARCHAR(255),          -- User ID who resolved
    resolution_notes     TEXT,
    org_id               VARCHAR(36)  NOT NULL,
    CONSTRAINT fk_review_tasks_incoming
        FOREIGN KEY (incoming_profile_id) REFERENCES profiles (profile_id) ON DELETE CASCADE,
    CONSTRAINT fk_review_tasks_candidate
        FOREIGN KEY (candidate_profile_id) REFERENCES profiles (profile_id) ON DELETE CASCADE,
    CONSTRAINT uq_review_task_profiles UNIQUE (incoming_profile_id, candidate_profile_id),
    INDEX idx_review_tasks_org_id_status (org_id, status),
    INDEX idx_review_tasks_created (created_at DESC),
    INDEX idx_review_tasks_incoming (incoming_profile_id, status),
    -- Serves the candidate half of "incoming_profile_id IN (...) OR candidate_profile_id IN (...)", and the
    -- foreign key on candidate_profile_id.
    INDEX idx_review_tasks_candidate (candidate_profile_id, status)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;

-- Pairs an administrator explicitly rejected as non-matches. Kept separate from
-- review_tasks so profile edits and deletes can clear them without losing task history.
CREATE TABLE IF NOT EXISTS rejection_pairs
(
    id              VARCHAR(255) NOT NULL PRIMARY KEY,
    -- Lower id first, so one row is one pair whichever way the review task ran.
    profile_id_1    VARCHAR(36)  NOT NULL,
    profile_id_2    VARCHAR(36)  NOT NULL,
    -- The evidence the rejection was made against; reconsidered only when later evidence
    -- is materially stronger. See "Rejections" in docs/concepts/unification-rules.md.
    match_score     DECIMAL(5,4),
    score_breakdown LONGTEXT DEFAULT ('{}'),
    rejected_by     VARCHAR(255),
    rejected_at     DATETIME(6)  NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    org_id          VARCHAR(255) NOT NULL,
    CONSTRAINT uq_rejection_pair UNIQUE (profile_id_1, profile_id_2),
    INDEX idx_rejection_pairs_org_id_p1 (org_id, profile_id_1),
    INDEX idx_rejection_pairs_org_id_p2 (org_id, profile_id_2)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin;
