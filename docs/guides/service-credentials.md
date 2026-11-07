# Service Credentials

CDS calls the Identity Server management APIs on behalf of each organization it
serves: it reads the claim dialects to build the profile schema, resolves
applications, reads the organization tree, and looks up users over SCIM. These
are service-to-service calls, from CDS to the Identity Server. They need an access
token, and CDS gets it with the client credentials of an application in the
Identity Server: its service credentials.

This guide is about where those credentials come from.

---

## Why one configured pair is not enough

An OAuth client in the Identity Server belongs to one tenant, and can only
authenticate at the token endpoint of that tenant. CDS builds a token endpoint per
organization (`https://<is>/t/<org>/oauth2/token`), so the configured
`client_id` / `client_secret` works for exactly one organization: the one its
application was created in. Every other organization fails at the token call.

That is fine where a deployment serves one organization. An Identity Server with
several tenants needs an application per tenant.

---

## Who creates the application

**The Identity Server does.** When an organization enables CDS through the
Identity Server (`PATCH /api/server/v1/configs/cds`), the CDS extension in the
Identity Server creates a machine-to-machine application named
`Customer Data Service` for that organization, authorizes it for the management
APIs CDS calls, and sends its credentials to CDS. Then it enables CDS for the
organization. An organization that already has the application keeps it: its
secret is renewed and sent when the organization turns CDS on from off.

The application is the Identity Server's own. Reserve its name so that tenant
administrators cannot change or delete it, and the Console does not list it.
Both lists apply to every tenant, and each replaces its default, so keep the
default entries:

```toml
[system_applications]
read_only_apps = ["Console", "Customer Data Service"]
system_portals = ["Console", "My Account", "Customer Data Service"]
```

CDS never creates applications and never holds Identity Server administrator
credentials. The extension already pushes profiles, claims and organizations to
CDS over the `/sync` endpoints with the sync admin credentials
(`auth_server.admin_username` / `admin_password`), and the service credentials
travel the same way.

```
Identity Server (CDS extension)                             CDS
  │ (creates the application, or renews its secret if CDS is turning on from off)
  │ POST /t/{org}/cds/api/v1/service-credentials/sync ──────▶ token request to /t/{org}/oauth2/token
  │      {event: SERVICE_CREDENTIALS_UPDATED,                  stores only if a token is issued
  │       client_id, client_secret}
  │ PATCH /t/{org}/cds/api/v1/config {cds_enabled} ─────────▶ initial schema sync, with the new credentials
```

The endpoint accepts HTTP Basic with the sync admin credentials only, and only on
a primary organization path. It is write-only: CDS never returns the credentials.
The API is in [`api/customer-data-service.yaml`](../../api/customer-data-service.yaml).

---

## Other identity providers

The endpoint and the store do not depend on WSO2 IS. CDS asks the adapter of the
identity provider (`idp.type`) to verify a pair before it stores it. An adapter
takes part by implementing `idp.ServiceCredentialsVerifier`; the WSO2 IS adapter
does it with a token request at the token endpoint of the organization. The
client of the identity provider reads the stored pair with `servicecreds.Get`
when it fetches a token.

An identity provider whose adapter does not implement it sends no credentials, so
CDS refuses an encryption key for it at start.

Token introspection goes through the adapter as well, when it implements
`idp.TokenIntrospector`. The WSO2 IS adapter introspects a token of an
organization with that organization's credentials, at its own introspection
endpoint (`/t/{org}/oauth2/introspect`). An organization without credentials,
such as a sub organization, uses the configured introspection client, as before.

---

## Turning it on

```yaml
idp:
  type: "wso2is"
  service_credentials:
    encryption_key: "${SERVICE_CREDENTIALS_ENCRYPTION_KEY}"
```

```bash
openssl rand -base64 32    # the value for SERVICE_CREDENTIALS_ENCRYPTION_KEY
```

On PostgreSQL, run
[`dbscripts/migrations/003-service-credentials-postgres.sql`](../../dbscripts/migrations/003-service-credentials-postgres.sql)
first. SQLite creates the table at start.

The key is the switch. Without it, the endpoint answers `400 CDS-16004`, the
Identity Server sends no credentials, and CDS uses the configured pair for every
organization, exactly as before. The server does not start when the key is set and
any of these is true:

- The key is not usable.
- The identity provider sends no credentials.
- The `org_service_credentials` table cannot be read, as on PostgreSQL before the
  migration.

---

## How the secret is stored

Encrypted, with AES-256-GCM, under `encryption_key`. Each row carries the nonce and
the ID of the key that encrypted it, so a secret encrypted under a key the
deployment no longer holds is reported as exactly that rather than as a corrupt row.

**It is not hashed, and cannot be.** CDS is the OAuth *client* here: it presents
this secret to the token endpoint on every token request. Hashing is for a secret
you verify, not one you send, and a hash cannot be presented. What CDS holds has to
be recoverable, so the protection is the key living outside the database. A
database dump on its own yields nothing usable against the Identity Server.

Rotating the key is not yet supported: there is no re-encryption pass. A row
encrypted under a key the deployment no longer holds is logged as an error, and the
organization falls back to the configured pair. To recover from that, from a
restored database, or from a secret regenerated in the Console, turn CDS off and
on again for the organization. Turning it on renews the secret and sends it again.

---

## Where the credentials are used

`FetchToken` uses the organization's own credentials where a row exists, and falls
back to the configured `client_id` / `client_secret` where none does. Token
introspection does the same with the configured introspection client (see
[Other identity providers](#other-identity-providers)). A deployment
without an encryption key does not read the table at all, so its token path is
unchanged.
