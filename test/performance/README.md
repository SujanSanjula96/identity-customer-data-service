# CDS performance tests

These artifacts run the performance test scenarios of the Customer Data Service (CDS)
with Apache JMeter. Each scenario is a use case. The structure follows
[wso2/performance-is](https://github.com/wso2/performance-is).

## Layout

```
test/performance/
  common/
    jmeter/
      perf-test-cds.sh      scenario list and the run loop
      generate-plans.py     makes the .jmx files of T2 to T7
      profile/              the profile use cases (T1, T2, T5, T6)
      sync/                 the IS sync use cases (T3, T4)
      unification/          the unification load (T7)
    tools/
      summarize.py          makes one summary line from a JMeter result file
      data-gen.py           makes the seed profiles and the input rows
      cds-admin.py          sets the rule sets, the snapshot, and the CDS restart
      queue-monitor.sh      records the unification queue (ActiveMQ)
      queue-stats.py        makes the queue summary
      pg-stats-diff.py      compares two pg_stat_statements files
      t8-reindex.sh         the T8 scenario
    cloudformation/
      cds-performance.yaml  the AWS deployment (all three patterns)
    deployment/
      setup/                node setup scripts and their configuration templates
      run-performance-tests.sh   runs the scenarios on the bastion (JMeter) node
    common-functions.sh     SSH, SCP, and stack functions for the operator machine
    start-performance.sh    creates the deployment, runs the tests, deletes the deployment
    stop-performance.sh     deletes a deployment that stayed
  single-node/              start-performance.sh for 1 CDS node and 1 IS node
  two-node-cluster/         start-performance.sh for 2 CDS nodes and 2 IS nodes
  three-node-cluster/       start-performance.sh for 3 CDS nodes and 2 IS nodes
  results/                  output of a local run (not in git)
```

## Scenarios

Each test case (T1 to T8) is one use case of CDS. `perf-test-cds.sh -l` lists the
scenarios.

| Name | Test case | Load | Measured |
|------|-----------|------|----------|
| `t1_anonymous_profile_creation` | T1. A new visitor arrives. `POST /profiles` with no cookie. | users | `1 Create Profile` |
| `t2_profile_update` | T2. `PATCH /profiles/{id}` with 2 traits (no rule attribute). | users | `1 Update Profile` |
| `t3_registration_sync` | T3. IS sends `POST_ADD_USER` (Basic authentication, no call to IS). | users | `1 Registration Sync` |
| `t4_login_sync` | T4. Register, anonymous profile, activity, `AUTHENTICATION_SUCCESS`, then poll until the merge shows. | users | `3 Login Sync`, `5 Time To Merge` |
| `t5_profile_link` | T5. Anonymous profile, add rule attributes, `POST /profiles/link`. Rule set R1, no matching data. | users | `3 Link Profile` |
| `t6_profile_link_fuzzy` | T6. As T5, with rule set R4. 20 % of the rows match a seed profile with typos. | users | `3 Link Profile` |
| `t7_unification_r1` to `_r5` | T7. Anonymous profiles with rule attributes at a fixed arrival rate: 20 % exact, 20 % fuzzy, 60 % no match. | rate | `1 Create Profile`, and the queue statistics |
| `t7_unification_r4_not_decisive` | T7 with rule set R4 and `deterministic_match_decisive` off. | rate | as T7 |
| `t8_rule_activation_reindex` | T8. Backfill of a new exact rule and a new fuzzy rule, a backfill during T1 load, and the index removal. | script | `t8-reindex.jsonl`, `t8-load.csv` |

**Load.** In the users mode, each level is a number of concurrent users (`-c`). In the
rate mode, each level is an arrival rate in jobs for each second (`-t`, default
`50 100 250 500 1000 drain`). The `drain` level sends 2000 jobs for each second for 30
seconds. After each rate level, the runner waits until the unification queue is empty.

**Rule sets.** All rule attributes are traits, so that the tests do not depend on the IS
claims. `tools/cds-admin.py` makes them.

| Rule set | Exact rules | Fuzzy rules |
|----------|-------------|-------------|
| R1 | email | none |
| R2 | none | first name |
| R3 | email | first name |
| R4 | email, phone | first name, address |
| R5 | email, phone, nic | first name, last name, address |

**Seed and snapshot.** Before the tests, `run-performance-tests.sh` makes the seed
profiles through `POST /profiles` (`-S`, default 100,000: 70 % permanent with rule values,
30 % anonymous) with rule set R5 active. Thus the unification index has all rule
attributes. Then it saves a snapshot of the CDS database. Before T5 to T8, the runner
restores the snapshot, sets the rule set in the database, and restarts CDS (the rules
cache and the fuzzy caches). The bastion node uses the control key of the run for the
restart.

The loader does not use the index backfill of CDS. T8 measures the backfill.

**Results.** In addition to `summary.csv`:

- `queue.csv`: the unification queue each 10 seconds (size, enqueue count, dequeue count,
  consumers), from the ActiveMQ console (Jolokia).
- `queue-summary.csv`: for each level with a data file, the enqueue and dequeue rates,
  the worker capacity (the dequeue rate while the queue has a backlog), the queue growth,
  and the drain time.
- `unification-counts.jsonl`: for each level, the new profiles that merged, that are in a
  review task, or that had no action.

**Groups.** Select scenarios with `-i` (a comma list of name parts). For example:

```
-- -i t1_,t2_,t3_,t4_,t5_           group A
-- -i t8_,t7_                       group B
-- -i t6_                           group C
```

## Local run

### 1. Tools

| Tool | Version | Used for |
|------|---------|----------|
| Apache JMeter | 5.6.3 or later | The load |
| Python | 3.8 or later | The summary |
| Java | 21 | JMeter and the identity server |
| `go`, `maven`, `jq` | | `scripts/local-setup/script.sh` |
| Docker | | PostgreSQL for the local setup (`--db postgres`) |

On macOS, `brew install jmeter` installs JMeter.

### 2. A CDS and an identity server

The tests need a CDS, an identity server that issues the tokens, and a database.
`scripts/local-setup/script.sh` makes all of them. See
[docs/guides/local-development.md](../../docs/guides/local-development.md).

```bash
./scripts/local-setup/script.sh up --db postgres
```

The first run builds the identity server pack and takes 10 to 30 minutes. Later runs use
`start`, which takes about 20 seconds.

If the ports 8900, 9443, or 5432 are in use, give the setup its own ports and its own
work directory:

```bash
./scripts/local-setup/script.sh up --db postgres --work-dir ../.perf-dev \
  --cds-port 8910 --is-offset 10 --pg-port 5442 --pg-container cds-perf-postgres
```

Then use `-P 8910 -Q 9453` in the run command below.

The setup gives the items that the tests need:

- CDS is enabled for the organization `carbon.super`.
- A client application with the audience `iam-cds` and the CDS scopes. The scenario above
  needs `internal_cds_profile_create` and `internal_cds_profile_view`.
- The client ID and the client secret of that application, in `<work-dir>/state.env`.

The load client does not need a client certificate. CDS does not request one, and JMeter
accepts the self-signed server certificates.

### 3. Run

A short run to check the setup (1 minute for each concurrency value):

```bash
./test/performance/common/jmeter/perf-test-cds.sh \
  -s .local-dev/state.env -c "10 50" -d 1 -w 0.25
```

A full run with the default values (50, 100, 150, 300, and 500 users, 15 minutes each,
5 minutes of warm-up):

```bash
./test/performance/common/jmeter/perf-test-cds.sh -s .local-dev/state.env
```

`perf-test-cds.sh -h` shows all options. The credentials can also come from the
environment (`CLIENT_ID` and `CLIENT_SECRET`), or you can give an access token in
`CDS_TOKEN`.

### 4. Results

```
test/performance/results/
  summary.csv                       one line for each scenario and concurrency value
  <scenario>/<users>_users/
    results.jtl                     all samples
    jmeter.log
```

`summary.csv` has these columns: samples, error %, average, standard deviation,
95th percentile, 99th percentile (all in ms), and throughput (requests for each second).
The samples in the warm-up period are not in the summary.

### 5. Before each run

- **Reset the data.** The scenario above creates profiles and cookies, so the database
  grows during a run. For results that you can compare, start each run from the same data.
  With the local setup and SQLite, `down`, delete `<work-dir>/cds-home/repository/database/`,
  and `up` again.
- **Set the CDS log level to WARN.** The local setup starts CDS at the DEBUG level by
  default, and CDS writes many log lines for each request. Use `--cds-log-level WARN` with
  the local setup.
- **Check the token lifetime.** Each JMeter run gets one token at the start. The default
  lifetime in the identity server is 3600 seconds, which is sufficient for a 15 minute run.

## Limits of a local run

A local run shows that the scripts work, and it finds large problems. Its numbers are not
capacity values:

- CDS, the identity server, the database, and JMeter share one machine.
- The local setup uses the in-memory queue and one CDS node.
- The identity server uses its default H2 databases.
- Each CDS request with a token calls the introspection endpoint of the identity server,
  so the identity server has a large effect on the results.

## AWS run

The AWS run gives the published results. It follows the deployment patterns of
[wso2/performance-is](https://github.com/wso2/performance-is): one CloudFormation stack for
each run, set up with shell scripts over SSH, and deleted at the end.

> **Warning:** `start-performance.sh` creates AWS resources that cost money. Check the
> prices of the instance types in your region before a run. Run the script with `-p`
> first. A dry run creates nothing.

### Deployment

| Node | Default type | Role |
|------|--------------|------|
| Bastion | `c6i.xlarge` | The only node with a public IP. Runs JMeter, as in performance-is. |
| nginx | `c6i.xlarge` | The load balancer at `https://cdslb` (CDS) and `https://wso2is:9443` (IS). Also used with one node. |
| CDS 1 to 3 | `c6i.large` | CDS on port 8900, as a systemd service |
| ActiveMQ | `c6i.large` | ActiveMQ Classic 5.18, STOMP on port 61613 |
| IS 1 and 2 | `c6i.xlarge` | WSO2 Identity Server on port 9443, with the CDS extension bundles. Two nodes form a cluster. |
| CDS database | `db.m6i.xlarge` | RDS PostgreSQL, `max_connections` 500 |
| IS database | `db.m6i.large` | RDS PostgreSQL |

The number of IS nodes is the `ISNodeCount` parameter of the stack (1 or 2), and the `-i`
option of `start-performance.sh`. The pattern folders set it: 1 for `single-node`, and 2
for `two-node-cluster` and `three-node-cluster`. With 2 IS nodes:

- The IS nodes share the IS database, and form a cluster with the well-known address
  membership on port 4000, as in the two node pattern of performance-is.
- nginx sends each request to the next IS node (round robin). The CDS and JMeter requests
  to IS use client credentials and token validation, so they do not need sticky sessions.
- The deployment has one ActiveMQ broker. CDS connects to one broker address, and the CDS
  worker on each CDS node reads the same queue.

All patterns send the IS requests through nginx, so that all patterns have the same path.

All nodes and both databases are in one private subnet in one availability zone. The
private nodes use a NAT gateway for the package downloads. SSH to the bastion node is open
only to the CIDR that you give with `-a`. The private key stays on the operator machine:
the scripts reach the private nodes through the bastion node with `ProxyJump`.

Differences from performance-is:

- One template for all patterns, with the `CDSNodeCount` and `ISNodeCount` parameters, and not one template
  for each pattern. The patterns cannot drift apart.
- EC2 instances, not Auto Scaling groups of one instance.
- The Ubuntu 24.04 image comes from the public SSM parameter. No AMI table for each region.
- New random passwords for each run, and a test CA for each run. The repository has no key
  and no password.

### TLS and trust

`start-performance.sh` makes a test CA in the run directory. The CA signs two certificates:

- `cds`: for nginx and all CDS nodes, with the names `cdslb` and `cds1` to `cds3` and their IPs.
- `is`: for nginx and all IS nodes, with the names `wso2is`, `is1`, and `is2` and their IPs.
  IS serves it from `[keystore.tls]`. The primary keystore of IS still signs the tokens. All
  IS nodes use the same primary keystore, so each node accepts the tokens of the other node.

CDS checks the host name of IS, so CDS trusts the CA (`trust_store.pem`) and calls
`https://wso2is:9443`. IS trusts the CA in `client-truststore.p12` and calls CDS at
`https://cdslb`. Both names point to nginx. nginx checks the CDS and the IS certificates. JMeter does not check certificates.
CDS does not request a client certificate, and `mtls_enabled` is false.

### Inputs

| Input | Option | Example |
|-------|--------|---------|
| AWS credentials and region | `AWS_PROFILE`, `AWS_REGION` | A profile that can create the resources of the template |
| EC2 key pair | `-k <file.pem>` | The key pair name is the file name without `.pem`, or `-K` |
| IS pack | `-n <zip>` | A pack whose Console knows `cds_host` |
| CDS extension bundles | `-x <dir>` | The `components` folder of an `identity-customer-data-service-extensions` build. Not for a pack that has the CDS bundles |
| CDS binary for linux/amd64 | `-b <file>` | See below |
| CDS source checkout of the binary (schema and configuration) | `-c <dir>` | The folder that you built the binary in |
| SSH source CIDR | `-a <cidr>` | The public IP of the operator machine with `/32` |
| User tag | `-q <tag>` | Your user name |

Build the CDS binary from the commit that you want to test:

```bash
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o <out dir>/cds-linux-amd64 ./cmd/server
```

Tools on the operator machine: `aws` (CLI v2), `jq`, `ssh`, `scp`, `openssl`, `zip`.

### Run

1. Do a dry run. It checks the inputs and the template, and shows the create command:

   ```bash
   ./test/performance/single-node/start-performance.sh -p \
     -k <key.pem> -n <wso2is.zip> -x <extensions dir> -b <cds binary> -c <cds source> \
     -a <your ip>/32 -q <tag> -o <run dir>
   ```

2. Set up a deployment and keep it, for a first check of the scripts (`-T`). Then run a
   short test by hand on the bastion node:

   ```bash
   ./test/performance/single-node/start-performance.sh -T <same options as above, without -p>
   ssh -F <run dir>/ssh_config bastion
   ~/cds-perf/run-performance-tests.sh -m ~/cds-perf/test-metadata.json -- -c 50 -d 2 -w 0.5
   ```

3. Delete the deployment:

   ```bash
   ./test/performance/common/stop-performance.sh <stack name>
   ```

4. Run the full tests. The script deletes the stack at the end, also after an error:

   ```bash
   ./test/performance/two-node-cluster/start-performance.sh <options> -- -c "50 100 150 300 500" -d 15 -w 5
   ```

The tests run on the bastion node under `nohup`. If the SSH connection stops, the tests
continue, but the stack stays. Then copy the results with
`scp -F <run dir>/ssh_config bastion:cds-perf/results.zip .` and delete the stack with
`stop-performance.sh`. `stop-performance.sh -l` lists the stacks that exist.

### Manual mode (a stack made in the AWS console)

Use this mode if the machine that runs the scripts has no AWS credentials:

1. Create the stack from `common/cloudformation/cds-performance.yaml` in the CloudFormation
   console.
2. Copy `common/deployment/manual-deployment.env.example` to the run directory. Run
   `chmod 600` on the copy, and write the stack outputs and the two database passwords in it.
3. Run the start script with `-O <that file>`, and with `-T` for the setup only. The script
   makes no AWS call. It sets up the nodes over SSH and runs the tests.
4. Delete the stack in the console.

```bash
./test/performance/single-node/start-performance.sh -T -O <run dir>/deployment.env -o <run dir> \
  -k <key.pem> -n <wso2is.zip> -x <extensions dir> -b <cds binary> -c <cds source>
```

For a long test, use `-D` in place of `-T`. The script sets up the nodes, starts the tests
on the bastion node, and exits. The tests do not need the operator machine. Later, run the
same command with `-R` in place of `-D`. It waits until the tests are done, and downloads
the results and the logs of all nodes. `-R` does not need `-n`, `-x`, `-b`, or `-c`.

### Smoke size (a check of the wiring)

Before the first performance run, do a smoke run. It uses the same template and scripts,
but small burstable instances.

- In the console, set the stack parameter `DeploymentSize` to `smoke`. Write
  `DeploymentSize=smoke` in the deployment file.
- With the AWS CLI, give `-z` to the start script.

| Node | Performance | Smoke |
|------|-------------|-------|
| CDS | c6i.large | t3.small |
| nginx | c6i.xlarge | t3.small |
| ActiveMQ | c6i.large | t3.medium |
| IS | c6i.xlarge | t3.large |
| Bastion (JMeter) | c6i.xlarge | t3.medium |
| CDS database | db.m6i.xlarge, 100 GB | db.t4g.small, 20 GB |
| IS database | db.m6i.large, 50 GB | db.t4g.small, 20 GB |

If you give no options after `--`, a smoke run does a short test: `-c "5 20" -d 2 -w 0.5`.
A smoke stack ignores the instance type parameters. Do not use smoke results as
performance data.

### Secrets

No deployment uses a product default secret. The start script makes new values for each
run in `secrets.env`: the IS admin, key store, `app_password`, and
encryption key values, and the database, CDS sync, and broker passwords. Each stack gets
its own IS primary key (`certs/is-primary.key`), and all IS nodes of the stack use it. The
IS trust store has no `wso2carbon` certificate, the IS SOAP admin services are off, the
IS agent user store (`AgentIdentity`) is on PostgreSQL and not on the H2 file with the user
`wso2carbon`, the ActiveMQ default users are removed, and nginx does not show its version.
CDS gets a random key for the service credentials (`SERVICE_CREDENTIALS_ENCRYPTION_KEY`).

A new setup of the same run directory keeps the values, because the IS database has data
that the encryption key protects.

### IS pack

An IS pack that has the CDS bundles needs no `-x`. Then IS gets only the short CDS section
(`common/deployment/setup/resources/is-cds-section-pack.toml`), because the pack adds the
event handlers and the CDS API resources itself. A newer IS blocks Basic authentication on
its APIs for a new organization. The setup allows it only for `provision-is.sh`. Then
`lock-is-basic-auth.sh` blocks it again and restarts IS.

### IS settings

IS has the same settings as the single node pattern of performance-is: the
`deployment.toml` values, the database names and SQL scripts, a 4 GB heap, and the GC log.
`common/deployment/setup/resources/is-deployment.toml` lists the differences and the reasons.

### Run directory

Use a directory outside the repository, for example `../cds-perf-runs/<name>`. The run
directory holds the secrets of the run, so the script makes it with mode 700.

```
<run dir>/
  secrets.env, state.env, cds.env    passwords and client secrets (mode 600)
  certs/                             the test CA and the certificates
  ssh_config, known_hosts            SSH to the nodes: ssh -F <run dir>/ssh_config bastion
  stack.env, stack-name.txt          the stack name and the node IPs
  stack-parameters.json              the stack parameters (mode 600)
  test-metadata.json                 instance types, CDS version, SHA-256 of the CDS binary
  results/                           summary.csv, JMeter files, database counts before and
                                     after, and pg_stat_statements
  nodes/<ip>.tgz                     sysstat data (each minute) and the logs of each node
```

### Monitor

- Each node records `sysstat` data each minute. Use `sar -f` on the files in `nodes/`.
- The RDS instances have Performance Insights (7 days, no extra cost).
- If the CPU of IS goes above 70 %, IS is the limit, not CDS. Use `-I c6i.2xlarge` and run
  the test again.
- If the CPU of the CDS database goes above 70 %, use `-E db.m6i.2xlarge`.

## Add a scenario

1. Add the `.jmx` file below `common/jmeter/`. Read these JMeter properties: `host`,
   `port`, `isHost`, `isPort`, `org`, `clientId`, `clientSecret`, `token`, `concurrency`,
   `rampUpPeriod`, and `time` (seconds).
   A plan with a data file reads `dataFile` (the output of `tools/data-gen.py incoming`),
   and a rate plan reads `rate`.
2. Add one line to the `scenarios` list in `perf-test-cds.sh`, with the name, the display
   name, the `.jmx` path, the measured labels, the mode, the prepare steps, and the data
   setting. The comment above the list describes the fields.
3. `common/jmeter/generate-plans.py` makes the plans T2 to T7. Change a plan there, then
   run it. Keep the shared parts (the token setup, the headers, and the assertions) the
   same as in the other plans.
