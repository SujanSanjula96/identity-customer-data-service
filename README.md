# identity-customer-data-service
Lightweight, extensible Customer Data Server built to power personalized experiences through unified user profiles and behavior insights.

# ⚡ Quickstart

### ✅ Prerequisites

- Go 1.26+
- cURL
- Docker — only if you run CDS on PostgreSQL or ActiveMQ. The inbuilt database
  and the in-memory queue need nothing extra.

---
### 🛠️ Configuration Steps

1. Register an M2M Application
    - Go to WSO2 Identity Server Console.
    - Create a Machine-to-Machine application.
    - Under **Api Resources** section:
        - Subscribe to Claim Management APIs.
    - Under **Protocol** section
        - Copy the Client ID and Client Secret.
        - Select JWT as Access Token type.
        - Add "iam-cds" to the Audience.

2. Create an Admin user
    - Go to WSO2 Identity Server Console .
    - Navigate to User Management section --> Users.
    - Create the user. Copy username and password
    - Navigate to Console Settings
        - Add administrator

3. Create the Environment File
    - Create a **dev.env** file under conf/repository/config/:
        - paste the Client secret copied
      ```bash
         ENV=dev
         LOG_LEVEL=DEBUG
         AUTH_SERVER_CLIENT_SECRET=<your_client_secret>
         AUTH_SERVER_ADMIN_PASSWORD=<your_admin_password>
      ```
   > A single application serves a single tenant, because an Identity Server OAuth
   > client can only authenticate at the token endpoint of the tenant it belongs to.
   > To have the Identity Server create one per organization instead, as each enables
   > CDS, see [Service Credentials](docs/guides/service-credentials.md).

4. Create Application that you want to use to access these APIs from
    - Register applications you want to integrate with the CDS.
    - Subscribe to required APIs from **API Resources** section:
        - Profiles
        - Profile Schema
        - Unification Rules
        - Consent Management

5. Obtain token against the application
    - Include required scopes for your use case.
    - required_scopes:
        - profile:create: "internal_cds_profile_create"
        - profile:update: "internal_cds_profile_update"
        - profile:view: "internal_cds_profile_view"
        - profile:delete: "internal_cds_profile_delete"

        - unification_rules:view: "internal_cds_unification_rule_view"
        - unification_rules:create: "internal_cds_unification_rule_create"
        - unification_rules:update: "internal_cds_unification_rule_update"
        - unification_rules:delete: "internal_cds_unification_rule_delete"

        - profile_schema:view: "internal_cds_profile_schema_view"
        - profile_schema:create: "internal_cds_profile_schema_create"
        - profile_schema:update: "internal_cds_profile_schema_update"
        - profile_schema:delete: "internal_cds_profile_schema_delete"

        - consent_category:view: "internal_cds_consent_category_view"
        - consent_category:create: "internal_cds_consent_category_create"
        - consent_category:update: "internal_cds_consent_category_update"
        - consent_category:delete: "internal_cds_consent_category_delete"

🎯 Include only the scopes you need in your token request.

```bash
`curl --location 'https://localhost:9443/oauth2/token' \
--header 'Authorization: Basic <Base64Encoded(CLIENT_ID:CLIENT_SECRET)>' \
--header 'Content-Type: application/x-www-form-urlencoded' \
--data-urlencode 'grant_type=client_credentials' \
--data-urlencode 'scope=internal_cds_profile_update internal_cds_profile_delete internal_cds_profile_view internal_cds_profile_create '`
```

6. Use the token obtained from token request call in Authorization header when calling CDM APIs.

`Authorization: Bearer <access_token>
`

---

## 📨 Message Queue

By default CDS uses an **in-memory queue** — no extra setup needed. For production or multi-instance deployments, switch to ActiveMQ for durable message delivery.

### Using ActiveMQ

```bash
docker run -d -p 61613:61613 --name activemq \
  -e ACTIVEMQ_ADMIN_LOGIN=admin \
  -e ACTIVEMQ_ADMIN_PASSWORD=admin \
  rmohr/activemq
```

Set `BROKER_PASSWORD` in your `dev.env` and update `deployment.yaml`:

```yaml
message_queue:
  type: "activemq"
  broker:
    addr: "localhost:61613"
    username: "admin"
    password: "${BROKER_PASSWORD}"
    profile_queue_name: "/queue/cds-profile-unification"
    schema_sync_queue_name: "/queue/cds-schema-sync"
```

To plug in a different broker (Kafka, RabbitMQ, SQS, etc.) see [docs/guides/extending-queue-providers.md](docs/guides/extending-queue-providers.md).

---

## 🗄 Database

CDS runs on any of four datasources, selected by `datasource.type` in
`repository/conf/deployment.yaml`.

| | Option A — inbuilt (SQLite) | Option B — PostgreSQL | Option C — MySQL | Option D — SQL Server |
|---|---|---|---|---|
| Setup | none | run a server, apply the DDL | run a server, apply the DDL | run a server, apply the DDL |
| `datasource.type` | `sqlite` (the default) | `postgres` | `mysql` | `mssql` |
| Best for | local development, demos, evaluation | production, multi-instance deployments | production, multi-instance deployments | production, multi-instance deployments |

### Option A: the inbuilt database

Set the datasource type and start the server. It creates the database file and
its schema on first start, so there is nothing to install and no DDL to run:

```yaml
datasource:
  type: "sqlite"
```

Leaving `datasource.type` out selects the inbuilt database as well. The shipped
`deployment.yaml` sets `postgres` explicitly, so edit it to switch.

The database lives at `repository/database/cds.db`, relative to the CDS home
directory. Set `datasource.sqlite.path` to put it elsewhere.

The inbuilt database is a single file written by a single process. Use PostgreSQL
instead when you need any of the following:

- More than one CDS instance against the same data. SQLite is not shared storage,
  so the Helm chart's default of two replicas requires PostgreSQL.
- Trigram-accelerated `co` (contains) and `sw` (starts with) profile filters.
  These work on the inbuilt database but scan the table, since SQLite has no
  `pg_trgm` or GIN indexes.
- Case-insensitive matching beyond ASCII. SQLite's `LIKE` — which stands in for
  PostgreSQL's `ILIKE` — is case-insensitive for ASCII characters only.
- `VARCHAR(n)` length enforcement, which SQLite does not apply.

### Option B: PostgreSQL

```bash
docker run -d -p 5432:5432 --name postgres \
  -e POSTGRES_USER=cdsuser \
  -e POSTGRES_PASSWORD=cdspwd \
  -e POSTGRES_DB=cdsdb \
  postgres
```

Apply the schema:

```bash
docker exec -i postgres psql -U cdsuser -d cdsdb < dbscripts/postgres.sql
```

Set `DB_PASSWORD=cdspwd` in your `dev.env`, and point the datasource at it:

```yaml
datasource:
  type: "postgres"
  hostname: "localhost"
  port: 5432
  username: "cdsuser"
  password: "${DB_PASSWORD}"
  name: "cdsdb"
  sslmode: disable
```

### Option C: MySQL

MySQL 8.0.17 or later is required. Start a server and apply the schema:

```bash
docker run -d -p 3306:3306 --name mysql \
  -e MYSQL_ROOT_PASSWORD=rootpwd \
  -e MYSQL_USER=cdsuser \
  -e MYSQL_PASSWORD=cdspwd \
  -e MYSQL_DATABASE=cdsdb \
  mysql:8.0

docker exec -i mysql mysql -ucdsuser -pcdspwd cdsdb < dbscripts/mysql.sql
```

Point the datasource at it. The `hostname`, `port`, `username`, `password` and `name`
settings are shared with PostgreSQL. `port` defaults to 3306 when left out.

```yaml
datasource:
  type: "mysql"
  hostname: "localhost"
  port: 3306
  username: "cdsuser"
  password: "${DB_PASSWORD}"
  name: "cdsdb"
  sslmode: disable
```

`sslmode` takes the PostgreSQL names. `disable` (or leaving it out) uses no TLS,
`require` encrypts without verifying the server certificate, and `verify-ca` and
`verify-full` verify it. Connection pool settings go under `datasource.mysql`, with
the same names as `datasource.postgres`.

Two behaviors differ from PostgreSQL. The schema uses a binary collation, so
matching is case-sensitive everywhere, including the `co`, `sw` and `ew` filters. And
`blocking_keys` is not partitioned, because MySQL does not allow a foreign key on a
partitioned table.

### Option D: SQL Server

SQL Server 2022 or later is required. Create a database with the binary collation the
schema expects, then apply the schema:

```bash
docker run -d -p 1433:1433 --name mssql \
  -e ACCEPT_EULA=Y \
  -e MSSQL_SA_PASSWORD='Cds_Passw0rd!' \
  mcr.microsoft.com/mssql/server:2022-latest

docker exec mssql /opt/mssql-tools18/bin/sqlcmd -C -S localhost -U sa -P 'Cds_Passw0rd!' \
  -Q "CREATE DATABASE cdsdb COLLATE Latin1_General_100_BIN2"

docker exec -i mssql /opt/mssql-tools18/bin/sqlcmd -C -S localhost -U sa -P 'Cds_Passw0rd!' \
  -d cdsdb < dbscripts/mssql.sql
```

Point the datasource at it. The shared settings apply, and `port` defaults to 1433:

```yaml
datasource:
  type: "mssql"
  hostname: "localhost"
  port: 1433
  username: "sa"
  password: "${DB_PASSWORD}"
  name: "cdsdb"
  sslmode: require
```

`sslmode` takes the same names as for MySQL. Connection pool settings go under
`datasource.mssql`.

The behaviors that differ from PostgreSQL are the ones listed for MySQL: matching is
case-sensitive, and `blocking_keys` is not partitioned. Two more apply to SQL Server.
It allows 2100 parameters in a statement, so CDS splits its large inserts into smaller
ones. And it rejects two cascading foreign keys from `review_tasks` to `profiles`, so
deleting a profile also deletes the review tasks that name it as a candidate, in the
same statement.

---

## 🏗 Build and Run

### 🛠 Step 1: Build the Product

```bash
make all
```

---

### ▶️ Step 2: Run the Product

```bash
cd target
unzip cds-1.0.0-m1-SNAPSHOT.zip
cd cds-1.0.0-m1-SNAPSHOT
./cds
```

---
