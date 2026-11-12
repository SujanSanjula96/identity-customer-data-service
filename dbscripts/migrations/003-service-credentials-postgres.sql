-- service credentials: schema delta for an existing CDS database.
--
-- Adds the org_service_credentials table of dbscripts/postgres.sql, and nothing
-- else. Purely additive, so it can run against a live database. Run it before you
-- set idp.service_credentials.encryption_key in deployment.yaml.
--
-- Re-runnable. Every statement is guarded, so applying it twice is a no-op.

BEGIN;

-- Credentials of the Identity Server application CDS calls the management APIs
-- with, one row per organization, sent by the Identity Server when the
-- organization enables CDS.
--
-- client_secret is sealed, never plaintext, and key_id names the key that sealed
-- it. It cannot be hashed: CDS presents this secret to the token endpoint on every
-- token request, and a hash cannot be presented.
CREATE TABLE IF NOT EXISTS org_service_credentials (
    org_id        VARCHAR(255) NOT NULL,
    org_handle    VARCHAR(255) NOT NULL,
    client_id     VARCHAR(255) NOT NULL,
    client_secret TEXT         NOT NULL,
    key_id        VARCHAR(64)  NOT NULL,
    PRIMARY KEY (org_id),
    UNIQUE (org_handle)
);

COMMIT;
