# Org ID migration

CDS keys its data by org ID, not by org handle. The APIs do not change: they keep `/t/{org_handle}/...` and
`org_handle` in request and response bodies. This guide tells you how to move an existing PostgreSQL deployment
from org handles to org IDs without downtime.

A fresh install needs no migration. It uses the default phase, `org_id_only`, and the schema of
`dbscripts/postgres.sql`.

This guide does not apply to the inbuilt SQLite database (see [SQLite](#sqlite)).

## What changes

Nine tables replace their org handle column with an org ID column:

| Table | Old column | New column |
|---|---|---|
| `profiles` | `org_handle` | `org_id` |
| `profile_reference` | `org_handle` | `org_id` |
| `profile_reference` | `reference_profile_org_handle` | `reference_profile_org_id` |
| `profile_schema` | `org_handle` | `org_id` |
| `unification_rules` | `org_handle` | `org_id` |
| `applications` | `org_handle` | `org_id` |
| `consent_categories` | `org_handle` | `org_id` |
| `cds_config` | `org_handle` | `org_id` |
| `profile_unification_modes` | `org_handle` | `org_id` |
| `profile_unification_triggers` | `org_handle` | `org_id` |

The `organizations` table keeps `org_handle`. It is the only map from handle to ID. CDS reads the org ID of a
handle from this table. If the table has no row for the handle, CDS reads the organization from the identity
server and stores it.

The identity resolution tables (`blocking_keys`, `review_tasks`, `rejection_pairs`) keep `org_handle`.

## The phase setting

One setting selects the behavior of the server:

```yaml
org_id_migration:
  phase: "dual_write"   # dual_write | read_org_id | org_id_only
```

| Phase | Writes | Reads, filters, conflict targets | Schema that the phase needs |
|---|---|---|---|
| `dual_write` | `org_handle` and `org_id` | `org_handle` | Step 1 is complete |
| `read_org_id` | `org_handle` and `org_id` | `org_id` | Step 4 is complete |
| `org_id_only` | `org_id` | `org_id` | Step 4 is complete |

Rules:

1. Move one phase forward at a time. Do not go from `dual_write` to `org_id_only` directly.
2. A change of the phase is a rolling restart, so the pods of two phases run together for a short time. Two
   adjacent phases can run together. Two phases that are not adjacent cannot.
3. Do not go back after a pod runs `org_id_only`. The rows that it writes have no org handle, so the
   `read_org_id` and `dual_write` phases cannot read them.
4. At start-up, CDS reads the database catalog. If the schema does not match the phase, CDS does not start. The
   error names each problem and the script that fixes it.

## Before you start

1. Make sure that the `organizations` table exists. If it does not exist, run
   `dbscripts/migrations/002-organization-management-postgres.sql`.
2. Make sure that the CDS client in the identity server can call
   `GET /t/{org_handle}/api/server/v1/organizations/self` for each organization.
3. Set `org_id_migration.phase` to `dual_write` in the configuration of the new release. The default is
   `org_id_only`, which does not start on a database from before the migration.

Run each script with `psql -v ON_ERROR_STOP=1 -f <script>`. Each script can run again: if a script stops, fix the
cause and run it again.

## Procedure

Start each step only after the previous step is complete on all pods and all tables.

### Step 1: Add the org ID columns

Run `dbscripts/migrations/003-org-id-add-columns-postgres.sql`.

The script adds a nullable column next to each old column. It changes only the catalog. The running release
ignores the new columns.

Rollback: drop the new columns.

### Step 2: Deploy the new release with `phase: dual_write`

The release writes the org handle and the org ID. It reads by org handle. Each organization that sends a request
gets its row in `organizations`.

Rollback: deploy the previous release.

### Step 3: Fill the org ID of the old rows (backfill)

The backfill is not part of the server. For each table of the migration:

1. Get the distinct handles that have no `organizations` row. Include organizations that disabled CDS, because
   they still have data.
2. Store each one in `organizations`, with the org ID that the identity server reports for it.
3. If the identity server does not know a handle, log it and skip it. Its rows keep a NULL `org_id`.
4. Update the rows in batches:

   ```sql
   UPDATE profiles p SET org_id = o.org_id
   FROM organizations o
   WHERE o.org_handle = p.org_handle AND p.org_id IS NULL;
   ```

5. Make sure that only the skipped handles have rows with a NULL `org_id`:

   ```sql
   SELECT org_handle, count(*) FROM profiles WHERE org_id IS NULL GROUP BY org_handle;
   ```

Caution: step 4 sets `org_id` to NOT NULL. Before step 4, delete or archive the rows of the skipped handles.

### Step 4: Move the constraints to the org ID

Run `dbscripts/migrations/004-org-id-switch-constraints-postgres.sql`.

The script:

1. Sets `org_id` to NOT NULL without a long lock.
2. Creates the indexes and unique keys on `org_id` with `CREATE INDEX CONCURRENTLY`.
3. Changes the primary key of `cds_config` to `(org_id, config)`. A unique index on `(org_handle, config)` stays,
   because the `dual_write` phase uses it.
4. Makes `org_handle` nullable.

The last query of the script lists invalid indexes. If it returns a row, a `CREATE INDEX CONCURRENTLY` failed.
Drop that index (`DROP INDEX CONCURRENTLY <name>`) and run the script again.

The `dual_write` pods keep running during and after this step.

Rollback: drop the new keys and indexes, restore the primary key of `cds_config` on `(org_handle, config)`, and
set `org_handle` to NOT NULL again.

### Step 5: Set `phase: read_org_id`

The release reads by org ID. It builds the profile `location` at read time, and it gets the handle of each
response from the `organizations` table. It still writes `org_handle`, so the `dual_write` pods can read every
row during the restart.

Rollback: set `phase: dual_write`.

### Step 6: Set `phase: org_id_only`

The release does not write `org_handle` or the stored `location`. All pods already read by org ID, so the
restart is safe.

Rollback: not possible (rule 3).

### Step 7: Drop the old columns

1. Back up the database.
2. Run `dbscripts/migrations/005-org-id-drop-org-handle-postgres.sql`.

After this step, only the `org_id_only` phase starts.

Rollback: restore the backup.

## Start-up errors

| Error | Cause | Action |
|---|---|---|
| `the organizations table is missing` | Migration 002 did not run. | Run `002-organization-management-postgres.sql`. |
| `<table>.org_id is missing` | Step 1 did not run. | Run step 1. |
| `<table>.org_id is nullable` | The phase is `read_org_id` or `org_id_only`, and step 4 did not run. | Run steps 3 and 4, or set `dual_write`. |
| `no valid index on ...` | Step 4 did not run, or one of its indexes is invalid. | Run step 4 again. |
| `<table>.org_handle is missing` | The phase is `dual_write` or `read_org_id`, and step 7 ran. | Set `org_id_only`. |

## SQLite

The inbuilt database has no migration. A new database file gets the final schema of `dbscripts/sqlite.sql`.

CDS does not migrate a database file from a release before the migration. That file does not work with the new
release.

Caution: When you delete the old file, you lose its data.

Delete the old file before you start the new release.

The inbuilt database ignores `org_id_migration.phase`. It always uses `org_id_only`.
