-- profile JSON indexes: schema delta for an existing CDS database.
--
-- Rebuilds the two GIN indexes on profiles.traits and profiles.identity_attributes
-- with the jsonb_path_ops operator class, as dbscripts/postgres.sql now defines them.
--
-- Each profile create and update writes these indexes. A jsonb_path_ops index is
-- smaller and cheaper to update than the default jsonb_ops index. It supports the
-- operators that CDS uses on these columns: @> (the eq profile filter) and @? (the
-- oldest reference lookup). It does not support the key-exists operators (?, ?|,
-- ?&), which no CDS query uses.
--
-- The script builds the new indexes before it drops the old ones, so the profile
-- search always has an index. CONCURRENTLY builds and drops without a lock that
-- blocks writes, so it can run against a live database. CONCURRENTLY cannot run in
-- a transaction block, so this script has no BEGIN and COMMIT.
--
-- Re-runnable. If a build fails, PostgreSQL keeps an INVALID index that IF NOT EXISTS
-- then skips: drop that index with DROP INDEX CONCURRENTLY, and run the script again.
--
-- Measure the profile search API before and after this change: the search reads
-- these indexes.

CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_profiles_traits_path_gin
    ON profiles USING GIN (traits jsonb_path_ops);

CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_profiles_identity_attributes_path_gin
    ON profiles USING GIN (identity_attributes jsonb_path_ops);

DROP INDEX CONCURRENTLY IF EXISTS idx_profiles_traits_gin;

DROP INDEX CONCURRENTLY IF EXISTS idx_profiles_identity_attributes_gin;
