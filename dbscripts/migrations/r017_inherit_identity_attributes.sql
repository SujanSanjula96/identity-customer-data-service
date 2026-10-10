-- R-017: a sub org inherits the identity attributes of its root and stores no copy.
--
-- Before R-017, the per-org initialization copied the identity attributes of the root into each
-- sub org. This script moves the references from each copy to the root attribute with the same
-- name, and then deletes the copies. CDS ignores the copies after R-017, so the script only cleans
-- up. It is safe to run more than once.
--
-- Run the steps in this order. A delete of a copy first makes the foreign key cascade delete the
-- unification rules and the consent attributes on it.
--
-- PostgreSQL: psql -v ON_ERROR_STOP=1 -f r017_inherit_identity_attributes.sql
-- SQLite:     sqlite3 -cmd 'PRAGMA foreign_keys = ON' cds.db < r017_inherit_identity_attributes.sql

BEGIN;

-- 1. Map each copy to the root attribute with the same name.
CREATE TEMP TABLE r017_map AS
SELECT sub.attribute_id AS old_id, root.attribute_id AS new_id
FROM profile_schema sub
JOIN organizations so ON so.org_handle = sub.org_handle
JOIN organizations ro ON ro.org_id = so.root_org_id
JOIN profile_schema root ON root.org_handle = ro.org_handle
    AND root.scope = 'identity_attributes' AND root.attribute_name = sub.attribute_name
WHERE sub.scope = 'identity_attributes' AND so.org_id <> so.root_org_id;

-- 2. Move the references of the unification rules.
UPDATE unification_rules
SET property_id = (SELECT new_id FROM r017_map WHERE old_id = unification_rules.property_id)
WHERE property_id IN (SELECT old_id FROM r017_map);

-- 3. Move the references of the consent category attributes.
UPDATE consent_category_attributes
SET attribute_id = (SELECT new_id FROM r017_map WHERE old_id = consent_category_attributes.attribute_id)
WHERE attribute_id IN (SELECT old_id FROM r017_map);

-- 4. Delete the copies. A copy with no root attribute of the same name has no IdP claim, so the
-- cascade deletes the rules and the consent attributes on it.
DELETE FROM profile_schema
WHERE scope = 'identity_attributes'
  AND org_handle IN (SELECT org_handle FROM organizations WHERE org_id <> root_org_id);

DROP TABLE r017_map;

COMMIT;
