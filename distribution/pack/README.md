# Customer Data Directory (CDD) @VERSION@

CDD builds unified customer profiles from the identity and behavioural data an
organization already holds, and serves them to the applications that
personalize on them. It runs as its own service beside WSO2 Identity Server.

Extracting this pack is the installation; there is nothing to run first.

## Getting started

1. **Fill in `repository/conf/secrets.env`.** The pack ships every value empty.
   Set at least these two before the first start:

   ```bash
   SERVICE_CREDENTIALS_ENCRYPTION_KEY=<generated for this installation>
   AUTH_SERVER_ADMIN_PASSWORD=<the admin_password of step 3>
   ```

   Generate the key with `openssl rand -base64 32`. Back it up and keep it the
   same; never reuse a key from another installation or from an example. Keep
   the file readable by the account that runs CDD and nobody else.

2. **Start CDD.**

   ```bash
   bin/start.sh        # bin\start.bat on Windows
   ```

   CDD runs in the foreground on `https://localhost:8900`; Ctrl+C stops it. It
   starts on an inbuilt database, so nothing else has to be installed. At start
   it warns about any secret from step 1 that is still empty.

   On macOS, if the zip was downloaded with a browser and extracted in Finder,
   clear the quarantine flag once: `xattr -dr com.apple.quarantine <this directory>`.

3. **Switch CDD on in the Identity Server**, by adding this to the Identity
   Server's `repository/conf/deployment.toml` and restarting it:

   ```toml
   [customer_data_service]
   enable = true
   server_url = "https://localhost:8900"   # the public port: the Console
   sync_url = "https://localhost:8901"     # the sync port: the Identity Server's syncs
   admin_username = "admin"
   admin_password = "<AUTH_SERVER_ADMIN_PASSWORD in secrets.env>"
   ```

   `sync_url` names the sync port (`sync.addr` in `repository/conf/deployment.yaml`).
   Without a `sync` block there, CDD serves the syncs on 8900: leave `sync_url`
   out, and the Identity Server sends them to `server_url`.

4. **Enable CDD for an organization** from the Identity Server Console.

The Console calls CDD as it is. An application of your own calls the CDD APIs
with a JWT access token that carries `iam-cds` in its audience.

## Before production

- Replace the default certificate: its private key is in every Identity Server
  7.3.0 download. See `repository/resources/security/README.md`.
- Move from the inbuilt database to PostgreSQL, MySQL, SQL Server or Oracle. The
  DDL is in `repository/dbscripts/`.
- Supply the secrets from a secret manager, or keep `secrets.env` readable by the
  CDD account only. A variable set in the environment wins over the file.

## Layout

```
bin/                            cdd and its launcher
repository/conf/                deployment.yaml and secrets.env
repository/database/            the inbuilt database, when one is used
repository/dbscripts/           DDL for an external database
repository/resources/security/  the TLS material
```

## Documentation

- [Installation guide](https://github.com/wso2/identity-customer-data-service/blob/@VERSION@/docs/guides/on-prem-installation.md):
  configuration, secrets, certificates, and running CDD as a service
- [Documentation](https://github.com/wso2/identity-customer-data-service/blob/@VERSION@/docs/README.md)
- [API reference](https://github.com/wso2/identity-customer-data-service/blob/@VERSION@/api/customer-data-service.yaml)

CDD is licensed under the Apache License 2.0; see `LICENSE.txt`.
