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

-- Schema for Oracle Database 21c and 23ai. It uses only what 21c has, so one script serves both: JSON is the
-- native JSON type, which 19c lacks, a boolean is NUMBER(1), and a timestamp is TIMESTAMP(6) WITH TIME ZONE
-- holding UTC.
--
-- Run it as the application user, in the pluggable database CDS connects to. It is one PL/SQL block, so a SQL
-- client runs it as a single unit (SQL*Plus and SQLcl: run the file; the closing "/" executes the block). Each
-- statement is skipped when its object exists already, so running the script again is safe.
--
-- Oracle stores an empty string as NULL. The columns that can hold an empty string, such as display_name,
-- application_identifier, purpose, attribute_type and unification_method, are therefore nullable here, and CDS
-- reads a NULL in them back as an empty string.
DECLARE
    PROCEDURE ddl(statement VARCHAR2) IS
    BEGIN
        EXECUTE IMMEDIATE statement;
    EXCEPTION
        WHEN OTHERS THEN
            -- 955: name already used. 1408: the column list is indexed already. 2275: the constraint exists.
            IF SQLCODE NOT IN (-955, -1408, -2275) THEN
                RAISE;
            END IF;
    END ddl;
BEGIN
    ddl(q'~CREATE TABLE profiles
    (
        profile_id          VARCHAR2(36 CHAR) NOT NULL PRIMARY KEY,
        user_id             VARCHAR2(255 CHAR),
        created_at          TIMESTAMP(6) WITH TIME ZONE DEFAULT FROM_TZ(SYS_EXTRACT_UTC(SYSTIMESTAMP), 'UTC') NOT NULL,
        updated_at          TIMESTAMP(6) WITH TIME ZONE DEFAULT FROM_TZ(SYS_EXTRACT_UTC(SYSTIMESTAMP), 'UTC') NOT NULL,
        location            VARCHAR2(255 CHAR),
        origin_country      VARCHAR2(255 CHAR),
        list_profile        NUMBER(1) DEFAULT 1 CHECK (list_profile IN (0, 1)),
        delete_profile      NUMBER(1) DEFAULT 0 CHECK (delete_profile IN (0, 1)),
        traits              JSON DEFAULT '{}',
        identity_attributes JSON DEFAULT '{}',
        org_id              VARCHAR2(255 CHAR) NOT NULL
    )~');
    ddl('CREATE INDEX idx_profiles_org_id_created_profile ON profiles (org_id, created_at, profile_id)');
    ddl('CREATE INDEX idx_profiles_org_id_user ON profiles (org_id, user_id)');

    ddl(q'~CREATE TABLE profile_reference
    (
        profile_id               VARCHAR2(36 CHAR) NOT NULL PRIMARY KEY,
        profile_status           VARCHAR2(255 CHAR),
        reference_profile_id     VARCHAR2(36 CHAR),
        reference_reason         VARCHAR2(255 CHAR),
        org_id                   VARCHAR2(255 CHAR) NOT NULL,
        reference_profile_org_id VARCHAR2(255 CHAR)
    )~');
    ddl('CREATE INDEX idx_profile_reference_status_profile ON profile_reference (profile_status, profile_id)');
    ddl('CREATE INDEX idx_profile_reference_org_id_status_profile ON profile_reference (org_id, profile_status, profile_id)');
    ddl('CREATE INDEX idx_profile_reference_reference_profile ON profile_reference (reference_profile_id)');

    ddl(q'~CREATE TABLE profile_schema
    (
        attribute_id           VARCHAR2(255 CHAR) NOT NULL PRIMARY KEY,
        scope                  VARCHAR2(255 CHAR),
        attribute_name         VARCHAR2(255 CHAR) NOT NULL,
        display_name           VARCHAR2(255 CHAR),
        value_type             VARCHAR2(255 CHAR) NOT NULL,
        merge_strategy         VARCHAR2(255 CHAR) NOT NULL,
        application_identifier VARCHAR2(255 CHAR),
        mutability             VARCHAR2(255 CHAR) NOT NULL,
        multi_valued           NUMBER(1) DEFAULT 0 CHECK (multi_valued IN (0, 1)),
        canonical_values       CLOB DEFAULT '[]',
        sub_attributes         CLOB DEFAULT '[]',
        scim_dialect           VARCHAR2(255 CHAR),
        org_id                 VARCHAR2(255 CHAR) NOT NULL
    )~');
    ddl('CREATE INDEX idx_profile_schema_org_id_scope ON profile_schema (org_id, scope)');
    ddl('CREATE INDEX idx_profile_schema_org_id_attr_name ON profile_schema (org_id, attribute_name)');

    ddl(q'~CREATE TABLE unification_rules
    (
        rule_id            VARCHAR2(255 CHAR) NOT NULL PRIMARY KEY,
        rule_name          VARCHAR2(255 CHAR) NOT NULL,
        property_name      VARCHAR2(255 CHAR) NOT NULL,
        property_id        VARCHAR2(255 CHAR) REFERENCES profile_schema (attribute_id) ON DELETE CASCADE,
        priority           NUMBER(10) NOT NULL,
        is_active          NUMBER(1) NOT NULL CHECK (is_active IN (0, 1)),
        attribute_type     VARCHAR2(255 CHAR) DEFAULT 'PRIMITIVE_EXACT',
        unification_method VARCHAR2(255 CHAR) DEFAULT 'deterministic',
        match_strength     VARCHAR2(20 CHAR),
        mismatch_strength  VARCHAR2(20 CHAR),
        created_at         TIMESTAMP(6) WITH TIME ZONE DEFAULT FROM_TZ(SYS_EXTRACT_UTC(SYSTIMESTAMP), 'UTC') NOT NULL,
        updated_at         TIMESTAMP(6) WITH TIME ZONE DEFAULT FROM_TZ(SYS_EXTRACT_UTC(SYSTIMESTAMP), 'UTC') NOT NULL,
        org_id             VARCHAR2(255 CHAR) NOT NULL
    )~');
    -- match_strength is NULL to derive the strength from attribute_type, and a value overrides it. It has no
    -- column default, deliberately: see docs/concepts/unification-rules.md.
    ddl('CREATE INDEX idx_unification_rules_org_id_active_priority ON unification_rules (org_id, is_active, priority)');
    ddl('CREATE INDEX idx_unification_rules_property_id ON unification_rules (property_id)');

    ddl(q'~CREATE TABLE application_data
    (
        app_data_id      NUMBER(10) GENERATED BY DEFAULT AS IDENTITY PRIMARY KEY,
        profile_id       VARCHAR2(36 CHAR) REFERENCES profiles (profile_id) ON DELETE CASCADE,
        app_id           VARCHAR2(255 CHAR) NOT NULL,
        application_data JSON DEFAULT '{}'
    )~');
    -- Unique per profile and application, and not for a row with no profile, as in the other schemas.
    ddl(q'~CREATE UNIQUE INDEX unique_profile_app ON application_data
        (CASE WHEN profile_id IS NOT NULL THEN profile_id END, CASE WHEN profile_id IS NOT NULL THEN app_id END)~');
    ddl('CREATE INDEX idx_application_data_app_id ON application_data (app_id)');
    ddl('CREATE INDEX idx_application_data_profile_id ON application_data (profile_id)');

    ddl(q'~CREATE TABLE applications
    (
        app_id     VARCHAR2(255 CHAR) NOT NULL PRIMARY KEY,
        client_id  VARCHAR2(255 CHAR),
        created_at TIMESTAMP(6) WITH TIME ZONE DEFAULT FROM_TZ(SYS_EXTRACT_UTC(SYSTIMESTAMP), 'UTC') NOT NULL,
        updated_at TIMESTAMP(6) WITH TIME ZONE DEFAULT FROM_TZ(SYS_EXTRACT_UTC(SYSTIMESTAMP), 'UTC') NOT NULL,
        org_id     VARCHAR2(255 CHAR) NOT NULL
    )~');
    ddl(q'~CREATE UNIQUE INDEX uq_applications_org_client ON applications
        (CASE WHEN client_id IS NOT NULL THEN org_id END, CASE WHEN client_id IS NOT NULL THEN client_id END)~');

    ddl(q'~CREATE TABLE consent_categories
    (
        id                  NUMBER(10) GENERATED BY DEFAULT AS IDENTITY PRIMARY KEY,
        category_name       VARCHAR2(255 CHAR) NOT NULL,
        category_identifier VARCHAR2(36 CHAR) NOT NULL UNIQUE,
        purpose             VARCHAR2(255 CHAR),
        destinations        CLOB,
        is_mandatory        NUMBER(1) DEFAULT 0 NOT NULL CHECK (is_mandatory IN (0, 1)),
        org_id              VARCHAR2(255 CHAR) NOT NULL
    )~');

    ddl(q'~CREATE TABLE consent_category_attributes
    (
        id                     NUMBER(10) GENERATED BY DEFAULT AS IDENTITY PRIMARY KEY,
        category_id            VARCHAR2(36 CHAR) REFERENCES consent_categories (category_identifier) ON DELETE CASCADE,
        scope                  VARCHAR2(50 CHAR) NOT NULL,
        attribute_name         VARCHAR2(255 CHAR) NOT NULL,
        attribute_id           VARCHAR2(255 CHAR) REFERENCES profile_schema (attribute_id) ON DELETE CASCADE,
        application_identifier VARCHAR2(255 CHAR),
        CONSTRAINT uq_consent_category_attributes UNIQUE (category_id, scope, attribute_name, application_identifier)
    )~');

    ddl(q'~CREATE TABLE profile_consents
    (
        id             NUMBER(10) GENERATED BY DEFAULT AS IDENTITY PRIMARY KEY,
        profile_id     VARCHAR2(36 CHAR) REFERENCES profiles (profile_id) ON DELETE CASCADE,
        category_id    VARCHAR2(36 CHAR) REFERENCES consent_categories (category_identifier) ON DELETE CASCADE,
        consent_status NUMBER(1) NOT NULL CHECK (consent_status IN (0, 1)),
        consented_at   TIMESTAMP(6) WITH TIME ZONE DEFAULT FROM_TZ(SYS_EXTRACT_UTC(SYSTIMESTAMP), 'UTC') NOT NULL,
        CONSTRAINT uq_profile_consents UNIQUE (profile_id, category_id)
    )~');

    ddl(q'~CREATE TABLE profile_cookies
    (
        cookie_id  VARCHAR2(255 CHAR) NOT NULL PRIMARY KEY,
        profile_id VARCHAR2(36 CHAR) NOT NULL REFERENCES profiles (profile_id) ON DELETE CASCADE,
        is_active  NUMBER(1) DEFAULT 1 NOT NULL CHECK (is_active IN (0, 1))
    )~');
    ddl('CREATE INDEX idx_profile_cookies_profile_id ON profile_cookies (profile_id)');

    ddl(q'~CREATE TABLE cds_config
    (
        config VARCHAR2(255 CHAR) NOT NULL,
        value  VARCHAR2(500 CHAR),
        org_id VARCHAR2(255 CHAR) NOT NULL,
        CONSTRAINT pk_cds_config PRIMARY KEY (org_id, config)
    )~');

    -- Credentials of the Identity Server application CDS calls the management APIs with, one row per
    -- organization, sent by the Identity Server when the organization enables CDS.
    --
    -- client_secret is sealed, never plaintext, and key_id names the key that sealed it. It cannot be hashed:
    -- CDS presents this secret to the token endpoint on every token request, and a hash cannot be presented.
    ddl(q'~CREATE TABLE org_service_credentials
    (
        org_id        VARCHAR2(255 CHAR) NOT NULL PRIMARY KEY,
        client_id     VARCHAR2(255 CHAR) NOT NULL,
        client_secret CLOB NOT NULL,
        key_id        VARCHAR2(64 CHAR) NOT NULL
    )~');

    ddl(q'~CREATE TABLE organizations
    (
        org_id         VARCHAR2(255 CHAR) NOT NULL PRIMARY KEY,
        org_handle     VARCHAR2(255 CHAR) NOT NULL UNIQUE,
        org_name       VARCHAR2(255 CHAR),
        parent_org_id  VARCHAR2(255 CHAR),
        primary_org_id VARCHAR2(255 CHAR) NOT NULL,
        status         VARCHAR2(32 CHAR) NOT NULL,
        created_at     TIMESTAMP(6) WITH TIME ZONE DEFAULT FROM_TZ(SYS_EXTRACT_UTC(SYSTIMESTAMP), 'UTC') NOT NULL,
        updated_at     TIMESTAMP(6) WITH TIME ZONE DEFAULT FROM_TZ(SYS_EXTRACT_UTC(SYSTIMESTAMP), 'UTC') NOT NULL
    )~');
    ddl('CREATE INDEX idx_organizations_primary ON organizations (primary_org_id)');

    ddl(q'~CREATE TABLE resource_sharing_policy
    (
        policy_id         VARCHAR2(255 CHAR) NOT NULL PRIMARY KEY,
        resource_type     VARCHAR2(64 CHAR) NOT NULL,
        resource_id       VARCHAR2(255 CHAR) NOT NULL,
        owning_org_id     VARCHAR2(255 CHAR) NOT NULL,
        initiating_org_id VARCHAR2(255 CHAR) NOT NULL,
        CONSTRAINT uq_resource_sharing_policy UNIQUE (resource_type, resource_id, initiating_org_id)
    )~');
    ddl('CREATE INDEX idx_resource_sharing_policy_owning_org_type ON resource_sharing_policy (owning_org_id, resource_type)');

    ddl(q'~CREATE TABLE resource_sharing_policy_target
    (
        policy_id     VARCHAR2(255 CHAR) NOT NULL REFERENCES resource_sharing_policy (policy_id) ON DELETE CASCADE,
        target_scope  VARCHAR2(32 CHAR) NOT NULL,
        target_org_id VARCHAR2(255 CHAR) NOT NULL,
        CONSTRAINT pk_resource_sharing_policy_target PRIMARY KEY (policy_id, target_scope, target_org_id)
    )~');

    -- The largest table in the system. The absent surrogate key and the primary key's column order were settled
    -- by measurement: read "How the index is stored" in docs/concepts/how-unification-works.md before changing
    -- either. The key follows the candidate lookup: equality, equality, then IN.
    ddl(q'~CREATE TABLE blocking_keys
    (
        profile_id     VARCHAR2(36 CHAR) NOT NULL REFERENCES profiles (profile_id) ON DELETE CASCADE,
        attribute_name VARCHAR2(128 CHAR) NOT NULL,
        key_value      VARCHAR2(512 CHAR) NOT NULL,
        org_id         VARCHAR2(36 CHAR) NOT NULL,
        CONSTRAINT pk_blocking_keys PRIMARY KEY (org_id, attribute_name, key_value, profile_id)
    )~');
    -- Serves the per-profile delete during re-indexing, which has no key_value to prune on.
    ddl('CREATE INDEX idx_blocking_keys_org_id_profile ON blocking_keys (org_id, profile_id)');
    ddl('CREATE INDEX idx_blocking_keys_profile_id ON blocking_keys (profile_id)');

    ddl(q'~CREATE TABLE review_tasks
    (
        id                   VARCHAR2(255 CHAR) NOT NULL PRIMARY KEY,
        incoming_profile_id  VARCHAR2(36 CHAR) NOT NULL REFERENCES profiles (profile_id) ON DELETE CASCADE,
        candidate_profile_id VARCHAR2(36 CHAR) NOT NULL REFERENCES profiles (profile_id) ON DELETE CASCADE,
        match_score          NUMBER(5, 4) NOT NULL,
        status               VARCHAR2(50 CHAR) DEFAULT 'PENDING' NOT NULL,
        match_reason         CLOB,
        score_breakdown      CLOB DEFAULT '{}',
        created_at           TIMESTAMP(6) WITH TIME ZONE DEFAULT FROM_TZ(SYS_EXTRACT_UTC(SYSTIMESTAMP), 'UTC') NOT NULL,
        resolved_at          TIMESTAMP(6) WITH TIME ZONE,
        resolved_by          VARCHAR2(255 CHAR),
        resolution_notes     CLOB,
        org_id               VARCHAR2(36 CHAR) NOT NULL,
        CONSTRAINT uq_review_task_profiles UNIQUE (incoming_profile_id, candidate_profile_id)
    )~');
    ddl('CREATE INDEX idx_review_tasks_org_id_status ON review_tasks (org_id, status)');
    ddl('CREATE INDEX idx_review_tasks_created ON review_tasks (created_at DESC)');
    ddl('CREATE INDEX idx_review_tasks_incoming ON review_tasks (incoming_profile_id, status)');
    ddl('CREATE INDEX idx_review_tasks_candidate ON review_tasks (candidate_profile_id)');

    -- Pairs an administrator explicitly rejected as non-matches. Kept separate from review_tasks so profile
    -- edits and deletes can clear them without losing task history. The lower id is first, so one row is one
    -- pair whichever way the review task ran. The score and breakdown are the evidence the rejection was made
    -- against: see "Rejections" in docs/concepts/unification-rules.md.
    ddl(q'~CREATE TABLE rejection_pairs
    (
        id              VARCHAR2(255 CHAR) NOT NULL PRIMARY KEY,
        profile_id_1    VARCHAR2(36 CHAR) NOT NULL,
        profile_id_2    VARCHAR2(36 CHAR) NOT NULL,
        match_score     NUMBER(5, 4),
        score_breakdown CLOB DEFAULT '{}',
        rejected_by     VARCHAR2(255 CHAR),
        rejected_at     TIMESTAMP(6) WITH TIME ZONE DEFAULT FROM_TZ(SYS_EXTRACT_UTC(SYSTIMESTAMP), 'UTC') NOT NULL,
        org_id          VARCHAR2(255 CHAR) NOT NULL,
        CONSTRAINT uq_rejection_pair UNIQUE (profile_id_1, profile_id_2)
    )~');
    ddl('CREATE INDEX idx_rejection_pairs_org_id_p1 ON rejection_pairs (org_id, profile_id_1)');
    ddl('CREATE INDEX idx_rejection_pairs_org_id_p2 ON rejection_pairs (org_id, profile_id_2)');
END;
/
