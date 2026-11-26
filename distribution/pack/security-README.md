# TLS material

CDD serves HTTPS only and refuses to start without a certificate and a private
key, and it calls the Identity Server over HTTPS too, so this directory holds
both sides:

```
server.crt      the certificate CDD presents, PEM
server.key      its private key, PEM, mode 600
trust_store.pem the Identity Server certificates CDD trusts, PEM
```

`tls.cert_dir` in `repository/conf/deployment.yaml` points here. A relative
path is resolved against the CDD home.

## What this pack ships

All three files are **Identity Server 7.3.0's default key pair** — the
`wso2carbon` entry of its `wso2carbon.p12`. A default Identity Server 7.3.0
already trusts that certificate in its `client-truststore.p12`, and CDD trusts
it here, so the two call each other over TLS with no certificate set up on
either side.

That holds only while they reach each other as **`localhost`**: the
certificate is issued for `localhost` and nothing else. It suits a first
installation with both on one host. Containers, separate hosts, or any other
name need the certificates below.

## Replace it for production

**This key pair is a convenience for getting started, not a credential.** Its
private key is in every Identity Server 7.3.0 download, so anyone can present
themselves as this CDD — or as that Identity Server. `cdd start` warns while
it is in use.

Replace it on both sides:

1. Here: `server.crt` and `server.key` with a certificate issued for the hosts
   the Identity Server and the Console reach CDD on: those in its
   `[customer_data_service]` `server_url` and `sync_url`. A sync port reached
   on an internal name can present a key pair of its own (`sync.server_cert`).
2. Here: `trust_store.pem` with the certificate the Identity Server presents,
   or the CA that issued it.
3. On the Identity Server: import the new `server.crt`, or its CA, into
   `client-truststore.p12`, and restart it.

In a container, mount the files rather than changing the image: this directory
for CDD, and the truststore for the Identity Server.
