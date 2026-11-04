-- Organization management: schema delta for an existing CDS database.
--
-- Adds the organization and resource sharing tables of dbscripts/postgres.sql, and nothing
-- else. Purely additive, so it can run against a live database. Run it before you enable
-- organization_management in deployment.yaml.
--
-- Re-runnable. Every statement is guarded, so applying it twice is a no-op.

BEGIN;

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

CREATE INDEX IF NOT EXISTS idx_organizations_primary
    ON organizations (primary_org_id);

CREATE INDEX IF NOT EXISTS idx_resource_sharing_policy_owning_org_type
    ON resource_sharing_policy (owning_org_id, resource_type);

COMMIT;
