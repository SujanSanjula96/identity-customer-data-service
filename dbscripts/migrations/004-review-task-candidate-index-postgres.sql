-- review task candidate index: schema delta for an existing CDS database.
--
-- Adds the idx_review_tasks_candidate index of dbscripts/postgres.sql, and nothing
-- else. The review task queries look up a profile as the incoming profile or as the
-- candidate profile:
--
--   ... AND (incoming_profile_id IN (...) OR candidate_profile_id IN (...))
--
-- idx_review_tasks_incoming serves the first half only, so without this index the
-- OR reads the table. With it, PostgreSQL combines two index scans.
--
-- Purely additive. CONCURRENTLY builds the index without a lock that blocks writes,
-- so it can run against a live database. CONCURRENTLY cannot run in a transaction
-- block, so this script has no BEGIN and COMMIT.
--
-- Re-runnable. If a build fails, PostgreSQL keeps an INVALID index that IF NOT EXISTS
-- then skips: drop it with DROP INDEX CONCURRENTLY idx_review_tasks_candidate, and run
-- the script again.

CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_review_tasks_candidate
    ON review_tasks (candidate_profile_id, status);
