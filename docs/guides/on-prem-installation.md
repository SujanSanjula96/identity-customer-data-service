# Installing CDD on-premises

CDD runs as its own process beside an Identity Server. The two are
separate installations with separate lifecycles, and neither writes into the
other: CDD is extracted and configured on its own, and the Identity Server
carries its own side of the integration — the CDD extension bundles and the
configuration they need — switched on in its own `deployment.toml`.

So there is no step that reaches from one installation into the other, which
is also what lets either one run from an image or a read-only mount.

## Installing

Download the zip for your platform from the release page. Extracting it *is*
the installation; there is nothing to run to complete it.

```bash
unzip cdd-linux-amd64-1.2.0.zip -d /opt
cd /opt/cdd-linux-amd64-1.2.0

# Review the licence and required notices.
less LICENSE.txt

bin/start.sh
```

On Windows, extract the zip and run `bin\start.bat`. The Windows pack carries
`cdd.exe` and `start.bat` in `bin\` where the others carry `cdd` and
`start.sh`; everything else is the same. The launcher works from any directory.

On macOS, the executable is not notarized. If the zip was downloaded with a
browser and extracted in Finder, macOS blocks it as coming from an unidentified
developer. Clear the quarantine flag once, after extracting:

```bash
xattr -dr com.apple.quarantine cdd-darwin-arm64-1.2.0
```

A pack is extracted onto a machine where nothing else has been installed, so
it defaults to the inbuilt database and needs no database server to start.
Switch `datasource` in `repository/conf/deployment.yaml` to PostgreSQL, MySQL
or SQL Server for a production deployment — see the
[README](../../README.md#-database) for which to choose.

## Configuring CDD

`repository/conf/deployment.yaml` is the whole configuration. A pack's
defaults already match a default Identity Server on the same host:

| Setting | Pack default | Change it when |
|---|---|---|
| `auth_server.host`, `auth_server.port` | `localhost`, `9443` | the Identity Server is elsewhere |
| `auth_server.cookieDomain` | `localhost` | browsers reach CDD or the Identity Server by another name. It is the `Domain` of the `cds_profile` cookie and has to cover the hosts of both (see [Profiles](../concepts/profiles.md#the-browser-cookie)) |
| `addr.host`, `addr.port` | `127.0.0.1`, `8900` | CDD has to listen on another address |
| `sync.addr.host`, `sync.addr.port` | `127.0.0.1`, `8901` | the sync endpoints have to be on another address; without `sync.addr` they are served on `addr` |
| `server_url` | `https://localhost:8900` | the Identity Server reaches CDD by another name |
| `tls.cert_dir` | `repository/resources/security` | the TLS material is mounted elsewhere |
| `datasource` | the inbuilt database | in production — PostgreSQL, MySQL or SQL Server |
| `application_identifier_type` | `app_id` | the Console identifies applications by client ID; the two have to match |

Credentials are not written into it; see [Secrets](#secrets).

**Set `SERVICE_CREDENTIALS_ENCRYPTION_KEY` before switching CDD on.** When an
organization enables CDD, the Identity Server creates an application for CDD
in that organization and sends CDD its credentials. CDD calls the Identity
Server for that organization with them, and introspects its tokens with them,
and it keeps them only sealed with this key (`idp.service_credentials.encryption_key`).
Without a key it refuses them and says so at start; it can then call the
Identity Server only with a client pair of its own configured under
`auth_server`, which the pack does not set. Generate
one for this installation, and put it in `repository/conf/secrets.env` (see
[Secrets](#secrets)):

```bash
openssl rand -base64 32
```

Never reuse a key from another installation or from an example. Back it up and
keep it the same: credentials sealed with one key cannot be read with another.

## Switching CDD on in the Identity Server

Add one block to the Identity Server's `repository/conf/deployment.toml` and
restart it:

```toml
[customer_data_service]
enable = true
server_url = "https://localhost:8900"
sync_url = "https://localhost:8901"
admin_username = "admin"
admin_password = "<AUTH_SERVER_ADMIN_PASSWORD in secrets.env>"
```

`server_url` is the public address of CDD, `addr`: the Console calls CDD there.
`sync_url` is where the Identity Server sends its synchronisation calls: the
sync port, `sync.addr`. When CDD has no `sync.addr`, it serves those calls on
`addr` too; leave `sync_url` out, and the Identity Server sends them to
`server_url`. CDD logs at start which of the two it expects. `admin_username` and
`admin_password` are the credentials it presents on the CDD synchronisation
endpoints, so they have to match `auth_server.admin_username` and
`AUTH_SERVER_ADMIN_PASSWORD` on the CDD side.

Everything else the integration needs follows from `enable = true` in an
Identity Server release that carries the CDD integration: the event handlers,
the CDD API resources and their scopes, and the Console's API resource
collection.

CDD is then enabled per organization, in the Identity Server — from the
Console, or with `PATCH /api/server/v1/configs/cds`. That is also what runs
the organization's first profile-schema synchronisation and seeds its default
consent categories; until it has happened, the profile, schema and rule
endpoints refuse requests.

## Running CDD

```bash
bin/start.sh            # or bin/cdd start
bin/start.sh --debug    # logging at DEBUG
```

CDD runs in the foreground and logs to standard output. Ctrl+C stops it.
There is no background mode, and so no stop or status command. A start whose
address is already taken is refused up front rather than left to fail inside
the server's startup.

Running it as a service is left to whatever already runs services, each of
which wants exactly this — one process in the foreground, its logs on standard
output, and a signal to end it. With systemd:

```ini
[Unit]
Description=Customer Data Directory
After=network-online.target

[Service]
User=cdd
WorkingDirectory=/opt/cdd
ExecStart=/opt/cdd/bin/cdd start
Restart=on-failure

[Install]
WantedBy=multi-user.target
```

and its logs are then `journalctl -u cdd`. On macOS, a launchd job runs it the
same way. On Windows, CDD does not register as a Windows service itself, so run
`bin\cdd.exe start` under a service wrapper.

## Certificates

CDD and the Identity Server call each other over HTTPS, so each has to trust
the other's certificate.

A pack carries Identity Server 7.3.0's default key pair as its
`server.crt` and `server.key`, and trusts that certificate in
`trust_store.pem`. A default Identity Server 7.3.0 already trusts the same
certificate, so on one host the two talk to each other with no certificate set
up on either side.

The certificate is issued for `localhost` only. Containers, separate hosts, or
any other name need certificates issued for the names in use, on both sides —
see `repository/resources/security/README.md` in the pack.

**Its private key is in every Identity Server 7.3.0 download.** Anyone holding
it can present themselves as that CDD, or as that Identity Server. Replace it
before a deployment carries real traffic; `cdd start` warns while the default is
in use.

## Secrets

Credentials never go into `deployment.yaml`. They reach the runtime as the
environment variables the `${...}` placeholders in the configuration expand
from — `AUTH_SERVER_ADMIN_PASSWORD`, `SERVICE_CREDENTIALS_ENCRYPTION_KEY`,
`DB_PASSWORD` and the like.

The pack ships `repository/conf/secrets.env` with every one of them listed and
every value empty, one `KEY=VALUE` per line. Fill it in before the first start,
and keep it readable by the account that runs CDD and nobody else (the pack
ships it with mode 600). `cdd start` reads that file, and a variable already
set in the environment wins over it, so a deployment with a secret manager
supplies them from there and leaves the file empty.

| Secret | Needed | What it is |
|---|---|---|
| `SERVICE_CREDENTIALS_ENCRYPTION_KEY` | always | seals the service credentials of each organization; `openssl rand -base64 32`, generated for this installation |
| `AUTH_SERVER_ADMIN_PASSWORD` | always | the `admin_password` the Identity Server presents on the synchronisation endpoints |
| `DB_PASSWORD` | with an external database | the `datasource` password |
| `BROKER_PASSWORD` | with a message broker | the `message_queue.broker` password |

CDD starts with any of them empty, so a missing one would otherwise show only
as refused requests. At start it warns, naming the variable, when the admin
password or the encryption key is empty.

Keep a copy of the file, and of the key above all, outside the installation
directory. A new pack ships the file empty again, so moving to one means
copying the file over from the previous installation.

## Layout

```
<CDD_HOME>/
├── bin/
│   ├── cdd                         the runtime (cdd.exe on Windows)
│   └── start.sh                    runs it in the foreground (start.bat on Windows)
├── repository/
│   ├── conf/
│   │   ├── deployment.yaml         the configuration
│   │   └── secrets.env             the secrets, shipped empty for the operator to fill in, mode 600
│   ├── database/                   the inbuilt database, when one is used
│   ├── dbscripts/                  DDL for an external database
│   └── resources/security/         the TLS material
├── updates/
│   └── product.txt                 the update-service identity of this pack
├── version.txt
├── LICENSE.txt
└── README.md
```

## Air-gapped installations

A pack needs no network access to install or run beyond the Identity Server it
calls: copy the zip onto the machine, check it against its `.sha256`, and
extract it.
