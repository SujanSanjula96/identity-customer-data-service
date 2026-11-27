#!/usr/bin/env bash
#
# Installs the default TLS material a pack ships with: Identity Server
# 7.3.0's own default key pair.
#
# CDD and the Identity Server call each other over TLS, so each has to trust
# the other's certificate. Every Identity Server 7.3.0 download carries the
# same default key pair (alias wso2carbon in wso2carbon.p12) and already
# trusts its certificate (client-truststore.p12). A pack that presents that
# key pair, and trusts that certificate in turn, therefore talks to a default
# Identity Server 7.3.0 in both directions with no certificate set up on
# either side.
#
# The certificate is issued for localhost only, so this holds while CDD and
# the Identity Server reach each other as localhost: one host, as in a first
# installation. Anything else - containers, separate hosts - needs
# certificates issued for the names they use, on both sides.
#
# This is a convenience for getting started, NOT a production credential.
# The private key ships in every Identity Server download, so anyone can
# present themselves as a CDD using it. The runtime recognises the
# certificate by its fingerprint and says that it is still in use.
#
# No private key is committed here: the key pair is read at build time from
# the keystore committed to product-is for v7.3.0, which is the one the 7.3.0
# distribution ships. The keystore is checked against its SHA-256 and the
# certificate against its fingerprint, so a moved tag or a changed file fails
# the build rather than shipping something else. Set IS_KEYSTORE to a local
# copy of wso2carbon.p12 to build without network access.
#
#   default-cert.sh <security-dir>

set -euo pipefail

DIR="${1:?usage: default-cert.sh <security-dir>}"
mkdir -p "$DIR"

KEYSTORE_URL="https://raw.githubusercontent.com/wso2/product-is/v7.3.0/modules/distribution/src/repository/resources/security/wso2carbon.p12"
KEYSTORE_SHA256="d144705e501b2634aab8f0de54e243431e87e12ef5ff5057ad5727bf16e8bbe5"
KEYSTORE_PASSWORD="wso2carbon"
KEY_ALIAS="wso2carbon"

# The fingerprint the runtime matches on. Changing it means changing
# defaultFingerprints in internal/system/certs as well.
CERT_SHA256="2B:A3:61:A0:32:FF:D4:B3:4F:7D:F0:66:DE:72:B6:1C:5E:83:6A:09:F7:A7:BD:71:05:EB:87:B4:09:4D:97:12"

fail() { echo "default-cert: $*" >&2; exit 1; }

command -v openssl >/dev/null 2>&1 || fail "openssl is required to read the default key pair"

WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT
KEYSTORE="$WORK/wso2carbon.p12"

if [ -n "${IS_KEYSTORE:-}" ]; then
  cp "$IS_KEYSTORE" "$KEYSTORE" || fail "cannot read IS_KEYSTORE=$IS_KEYSTORE"
else
  command -v curl >/dev/null 2>&1 || fail "curl is required to fetch the keystore; or set IS_KEYSTORE"
  curl -fsSL --retry 3 -o "$KEYSTORE" "$KEYSTORE_URL" || fail "could not fetch $KEYSTORE_URL; or set IS_KEYSTORE"
fi

if command -v sha256sum >/dev/null 2>&1; then
  actual=$(sha256sum "$KEYSTORE" | cut -d' ' -f1)
else
  actual=$(shasum -a 256 "$KEYSTORE" | cut -d' ' -f1)
fi
[ "$actual" = "$KEYSTORE_SHA256" ] || fail "the keystore is not Identity Server 7.3.0's (sha256 $actual)"

# The keystore protects its certificates with RC2, which OpenSSL 3 reads only
# through its legacy provider. LibreSSL has no -legacy flag and needs none.
pkcs12() {
  openssl pkcs12 -legacy "$@" 2>/dev/null || openssl pkcs12 "$@" 2>/dev/null
}

pkcs12 -in "$KEYSTORE" -passin "pass:$KEYSTORE_PASSWORD" -nocerts -nodes > "$WORK/key.bags" \
  || fail "could not read the private key from the keystore"
openssl pkey -in "$WORK/key.bags" -out "$DIR/server.key" || fail "could not write the private key"

# The keystore holds the public CA roots as well, so the certificate is picked
# by its alias rather than taken as the first one.
pkcs12 -in "$KEYSTORE" -passin "pass:$KEYSTORE_PASSWORD" -nokeys > "$WORK/cert.bags" \
  || fail "could not read the certificates from the keystore"
awk -v alias="$KEY_ALIAS" '
  /friendlyName:/ { name = $2 }
  /-----BEGIN CERTIFICATE-----/ { keep = (name == alias) }
  keep { print }
  /-----END CERTIFICATE-----/ { if (keep) exit; name = "" }
' "$WORK/cert.bags" > "$WORK/cert.pem"
openssl x509 -in "$WORK/cert.pem" -out "$DIR/server.crt" 2>/dev/null || fail "no certificate with the alias $KEY_ALIAS"

fingerprint=$(openssl x509 -in "$DIR/server.crt" -noout -fingerprint -sha256 | cut -d= -f2)
[ "$fingerprint" = "$CERT_SHA256" ] || fail "unexpected certificate fingerprint $fingerprint"
[ "$(openssl x509 -in "$DIR/server.crt" -noout -pubkey)" = "$(openssl pkey -in "$DIR/server.key" -pubout)" ] \
  || fail "the private key does not belong to the certificate"

# CDD calls the Identity Server too, so it trusts the same certificate.
cp "$DIR/server.crt" "$DIR/trust_store.pem"

# The Identity Server presents the same key pair as its client certificate on
# the sync port, so CDD accepts it there once sync.require_client_cert is on.
# A file of its own, not the trust store: it names who may call the sync port,
# not which servers CDD calls.
cp "$DIR/server.crt" "$DIR/client_trust_store.pem"

# The key is readable by its owner alone, even inside the zip, so an extracted
# pack does not start out world-readable.
chmod 600 "$DIR/server.key"
chmod 644 "$DIR/server.crt" "$DIR/trust_store.pem" "$DIR/client_trust_store.pem"
