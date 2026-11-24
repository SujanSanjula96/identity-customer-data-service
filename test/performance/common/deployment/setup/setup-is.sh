#!/usr/bin/env bash
# Installs and starts the WSO2 Identity Server (IS) for the CDS performance
# tests. Runs on the IS node. start-performance.sh copies the bundle to
# ~/cds-perf and runs this script.
#
# The script does the same IS-side wiring as scripts/local-setup/script.sh,
# with these differences:
#   - IS uses PostgreSQL on its own RDS instance, not H2.
#   - The TLS certificate of IS comes from the test CA and has the host name
#     that CDS uses. CDS checks the host name.
#   - IS runs as a systemd service.
#
# The IS settings (deployment.toml, heap 4g, GC log, database names) are the
# same as in the single node pattern of wso2/performance-is.
#
# Bundle layout (~/cds-perf):
#   secrets.env                IS_DB_USER, IS_DB_PASS, IS_ADMIN_PASS, CDS_SYNC_USER, CDS_SYNC_PASS,
#                              IS_KEYSTORE_PASS, IS_APP_PASS, IS_ENCRYPTION_KEY
#   wso2is.zip                 the IS pack
#   extensions/*.jar           the four CDS extension bundles (not for a pack
#                              that has the CDS bundles)
#   certs/ca.crt, is.crt, is.key, is-primary.crt, is-primary.key
#   setup/resources/is-deployment.toml
#   setup/resources/is-cds-section.toml   (scripts/local-setup/templates/is-deployment.toml,
#                              or is-cds-section-pack.toml for a pack that has the CDS bundles)
#   setup/resources/create-is-databases.sql

set -euo pipefail

bundle_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
res_dir="$bundle_dir/setup/resources"
is_hostname="wso2is"
is_ip=""
is_db_host=""
cds_base="https://cdslb"
heap="4g"
cluster_members=""
jdbc_version="42.7.4"
carbon_home="$HOME/wso2is"

usage() {
    cat <<EOF
Usage: $0 -i <is_private_ip> -d <is_db_host> [-c <cds_base_url>] [-m <heap>] [-k "<ip> <ip>"]

  -i  The private IP of the IS node.
  -d  The endpoint of the IS RDS instance.
  -c  The base URL that IS uses to call CDS. Default: $cds_base.
  -m  The IS heap size. Default: $heap.
  -k  The private IPs of all IS nodes in the cluster, with spaces between
      them. Give it only for more than one IS node. The nodes use the
      well-known address (wka) membership on port 4000, as in the two node
      pattern of wso2/performance-is.
EOF
}

while getopts "i:d:c:m:k:h" opt; do
    case "$opt" in
        i) is_ip="$OPTARG" ;;
        d) is_db_host="$OPTARG" ;;
        c) cds_base="$OPTARG" ;;
        m) heap="$OPTARG" ;;
        k) cluster_members="$OPTARG" ;;
        h) usage; exit 0 ;;
        *) usage >&2; exit 2 ;;
    esac
done
[ -n "$is_ip" ] && [ -n "$is_db_host" ] || { usage >&2; exit 2; }

# shellcheck source=/dev/null
source "$bundle_dir/secrets.env"
: "${IS_DB_USER:?}" "${IS_DB_PASS:?}" "${IS_ADMIN_PASS:?}" "${CDS_SYNC_USER:?}" "${CDS_SYNC_PASS:?}"
: "${IS_KEYSTORE_PASS:?}" "${IS_APP_PASS:?}" "${IS_ENCRYPTION_KEY:?}"
# openssl reads the key store password from the environment (-passout env:).
export IS_KEYSTORE_PASS

# render TEMPLATE NAME=VALUE ... - the same function as in
# scripts/local-setup/script.sh. ${NAME} becomes its value. A placeholder with
# no value is an error. Placeholders with a dot, for example
# ${carbon.management.port}, are not changed.
render() {
    local tpl="$1"
    shift
    (
        local pair
        for pair in "$@"; do export "TPL_${pair%%=*}=${pair#*=}"; done
        awk '
          {
            out = ""
            while (match($0, /\$\{[A-Za-z_][A-Za-z_0-9]*\}/)) {
              name = substr($0, RSTART + 2, RLENGTH - 3)
              if (!(("TPL_" name) in ENVIRON)) {
                printf "no value for ${%s}\n", name > "/dev/stderr"
                exit 1
              }
              out = out substr($0, 1, RSTART - 1) ENVIRON["TPL_" name]
              $0 = substr($0, RSTART + RLENGTH)
            }
            print out $0
          }
        ' "$tpl"
    )
}

# The markers of the block that allows Basic authentication during the setup.
# lock-is-basic-auth.sh uses the same markers.
basic_auth_begin="# BEGIN cds-performance setup: Basic authentication for provision-is.sh"
basic_auth_end="# END cds-performance setup: Basic authentication for provision-is.sh"

toml_str() { printf '%s' "$1" | sed -e 's/\\/\\\\/g' -e 's/"/\\"/g'; }

echo "==> Stop a running IS"
sudo systemctl stop wso2is 2>/dev/null || true

echo "==> Extract the IS pack"
rm -rf "$carbon_home" "$HOME"/wso2is-*
unzip -q "$bundle_dir/wso2is.zip" -d "$HOME"
mv "$HOME"/wso2is-* "$carbon_home"

echo "==> PostgreSQL JDBC driver $jdbc_version"
jar="postgresql-$jdbc_version.jar"
url="https://repo1.maven.org/maven2/org/postgresql/postgresql/$jdbc_version/$jar"
curl -fsSL -o "/tmp/$jar" "$url"
expected="$(curl -fsSL "$url.sha1")"
actual="$(sha1sum "/tmp/$jar" | awk '{print $1}')"
[ "$expected" = "$actual" ] || { echo "SHA-1 check failed for $jar" >&2; exit 1; }
mv "/tmp/$jar" "$carbon_home/repository/components/lib/"

echo "==> IS databases on $is_db_host"
export PGPASSWORD="$IS_DB_PASS"
if psql -h "$is_db_host" -U "$IS_DB_USER" -d postgres -tAc "SELECT 1 FROM pg_database WHERE datname='IDENTITY_DB'" | grep -q 1; then
    echo "The IS databases exist. They are not changed."
else
    (cd "$carbon_home" && psql -q -h "$is_db_host" -U "$IS_DB_USER" -d postgres -v ON_ERROR_STOP=1 \
        -f "$res_dir/create-is-databases.sql" >/dev/null)
    echo "Created IDENTITY_DB, UM_DB, REG_DB, SESSION_DB (the performance-is names), and AGENT_IDENTITY_DB."
fi
unset PGPASSWORD

# An IS pack with the CDS integration has the CDS bundles in plugins. Then the pack and its
# "[customer_data_service] enable = true" give the bundles, the event handlers,
# and the API resources, and this script adds no bundle.
bundles_info="$carbon_home/repository/components/default/configuration/org.eclipse.equinox.simpleconfigurator/bundles.info"
pack_has_cds=false
grep -q '^org\.wso2\.identity\.customer\.data\.service\.client,' "$bundles_info" && pack_has_cds=true

echo "==> CDS extension bundles"
if $pack_has_cds; then
    echo "The IS pack has the CDS bundles. No bundle is added."
else
    dropins="$carbon_home/repository/components/dropins"
    count=0
    for b in org.wso2.identity.customer.data.service.client \
        org.wso2.identity.customer.data.service.event.handler \
        org.wso2.identity.customer.data.service.auth.listener \
        org.wso2.identity.customer.data.service.auth.handler; do
        found="$(find "$bundle_dir/extensions" -name "$b-*.jar" ! -name '*-sources.jar' ! -name '*-javadoc.jar' | head -1)"
        [ -n "$found" ] || { echo "No jar for $b in $bundle_dir/extensions" >&2; exit 1; }
        cp "$found" "$dropins/"
        count=$((count + 1))
    done
    echo "Deployed $count bundles."
fi

echo "==> Key stores and trust store"
# No store uses the default password wso2carbon or the default key of the pack.
sec="$carbon_home/repository/resources/security"
openssl pkcs12 -export -name perf-tls \
    -in "$bundle_dir/certs/is.crt" -inkey "$bundle_dir/certs/is.key" \
    -certfile "$bundle_dir/certs/ca.crt" \
    -out "$sec/perf-tls.p12" -passout env:IS_KEYSTORE_PASS
# The primary key store signs the tokens. All IS nodes get the same key from
# the operator machine. [keystore.internal] uses it too.
openssl pkcs12 -export -name perf-primary \
    -in "$bundle_dir/certs/is-primary.crt" -inkey "$bundle_dir/certs/is-primary.key" \
    -out "$sec/perf-primary.p12" -passout env:IS_KEYSTORE_PASS
chmod 600 "$sec/perf-tls.p12" "$sec/perf-primary.p12"
# The trust store: a new password, no default wso2carbon certificate, the test
# CA (IS calls CDS through nginx), and the new primary certificate.
ts="$sec/client-truststore.p12"
keytool -storepasswd -new "$IS_KEYSTORE_PASS" -keystore "$ts" -storetype PKCS12 -storepass wso2carbon
if keytool -list -alias wso2carbon -keystore "$ts" -storetype PKCS12 -storepass "$IS_KEYSTORE_PASS" >/dev/null 2>&1; then
    keytool -delete -alias wso2carbon -keystore "$ts" -storetype PKCS12 -storepass "$IS_KEYSTORE_PASS"
fi
keytool -importcert -noprompt -trustcacerts -alias perf-ca -file "$bundle_dir/certs/ca.crt" \
    -keystore "$ts" -storetype PKCS12 -storepass "$IS_KEYSTORE_PASS" >/dev/null
keytool -importcert -noprompt -alias perf-primary -file "$bundle_dir/certs/is-primary.crt" \
    -keystore "$ts" -storetype PKCS12 -storepass "$IS_KEYSTORE_PASS" >/dev/null

# CDS needs internal_cds_profile_link for POST /profiles/link
# (auth_server.required_scopes in the CDS configuration), but the template has
# only internal_org_cds_profile_link. Without the scope, the client
# application gets no link scope, and T5 and T6 get 403. Add the scope to the
# profile API resource, after its delete scope.
# A pack with the CDS integration has the scope in system-api-resource.xml.j2.
cds_section="$res_dir/is-cds-section.toml"
if ! $pack_has_cds && ! grep -q '^name = "internal_cds_profile_link"' "$cds_section"; then
    awk '
        { print }
        !done && $0 == "name = \"internal_cds_profile_delete\"" { found = 1; next }
        found && /^description = / {
            print ""
            print "[[api_resources.scopes]]"
            print "name = \"internal_cds_profile_link\""
            print "displayName = \"Profile link\""
            print "description = \"Link profile\""
            found = 0; done = 1
        }
        END { if (!done) exit 1 }
    ' "$cds_section" >"$cds_section.tmp" || { echo "Cannot add internal_cds_profile_link to $cds_section" >&2; exit 1; }
    mv "$cds_section.tmp" "$cds_section"
    echo "Added the scope internal_cds_profile_link to the profile API resource."
fi

echo "==> deployment.toml"
toml="$carbon_home/repository/conf/deployment.toml"
cp "$toml" "$toml.orig"
{
    render "$res_dir/is-deployment.toml" \
        "IS_HOSTNAME=$is_hostname" "IS_NODE_IP=$is_ip" \
        "IS_ADMIN_PASS=$(toml_str "$IS_ADMIN_PASS")" \
        "IS_DB_HOST=$is_db_host" "IS_DB_USER=$IS_DB_USER" "IS_DB_PASS=$(toml_str "$IS_DB_PASS")" \
        "IS_KEYSTORE_PASS=$(toml_str "$IS_KEYSTORE_PASS")" "IS_ENCRYPTION_KEY=$IS_ENCRYPTION_KEY" \
        "IS_APP_PASS=$(toml_str "$IS_APP_PASS")" \
        "IS_APP_PASS_HASH=$(printf '%s' "$IS_APP_PASS" | sha256sum | cut -d' ' -f1)"
    if [ -n "$cluster_members" ]; then
        members=""
        for m in $cluster_members; do members="$members${members:+, }\"$m:4000\""; done
        printf '\n[clustering]\nmembership_scheme = "wka"\nlocal_member_host = "%s"\nlocal_member_port = "4000"\nmembers = [%s]\n' \
            "$is_ip" "$members"
    fi
    printf '\n'
    render "$res_dir/is-cds-section.toml" \
        "TOML_BEGIN=# BEGIN cds-performance (from scripts/local-setup/templates/is-deployment.toml)" \
        "TOML_END=# END cds-performance" \
        "CDS_BASE=$(toml_str "$cds_base")" "IS_BASE=https://$is_hostname:9443" \
        "CDS_SYNC_USER=$(toml_str "$CDS_SYNC_USER")" "CDS_SYNC_PASS=$(toml_str "$CDS_SYNC_PASS")"
    # A newer IS blocks
    # Basic authentication on its APIs for a new organization (the compatibility
    # setting basicAuth.disableBasicAuth). provision-is.sh needs Basic
    # authentication one time, to make the first applications. Thus the setup
    # allows it, and lock-is-basic-auth.sh removes this block after
    # provision-is.sh and restarts IS. Then IS has the product default again.
    if grep -q 'basic_auth.disable_basic_auth' \
        "$carbon_home/repository/resources/conf/templates/repository/conf/compatibility-settings-metadata.json.j2" 2>/dev/null; then
        printf '\n%s\n%s\n%s\n%s\n' "$basic_auth_begin" \
            '[compatibility_setting.basic_auth.disable_basic_auth]' \
            'default_value = "false"' "$basic_auth_end"
    fi
} >"$toml"
chmod 600 "$toml"

echo "==> systemd service"
sudo tee /etc/systemd/system/wso2is.service >/dev/null <<EOF
[Unit]
Description=WSO2 Identity Server
After=network.target

[Service]
User=$USER
WorkingDirectory=$carbon_home
Environment=JAVA_HOME=/usr/lib/jvm/java-21-openjdk-amd64
Environment="JVM_MEM_OPTS=-Xms$heap -Xmx$heap"
# GC log and heap dump, as common/is/restart-is.sh of performance-is sets them.
# Java 21 uses -Xlog in place of -XX:+PrintGCDetails and -Xloggc.
Environment="JAVA_OPTS=-Xlog:gc*:file=$carbon_home/repository/logs/gc.log:time,uptime -XX:+HeapDumpOnOutOfMemoryError -XX:HeapDumpPath=$carbon_home/repository/logs/heap-dump.hprof"
ExecStart=$carbon_home/bin/wso2server.sh
LimitNOFILE=65535
TimeoutStopSec=120

[Install]
WantedBy=multi-user.target
EOF
sudo systemctl daemon-reload
sudo systemctl enable wso2is >/dev/null 2>&1
sudo systemctl start wso2is

echo "==> Wait for IS"
for _ in $(seq 1 120); do
    if [ "$(curl -sk -o /dev/null -w '%{http_code}' https://localhost:9443/api/health-check/v1.0/health)" = "200" ]; then
        break
    fi
    sleep 5
done
[ "$(curl -sk -o /dev/null -w '%{http_code}' https://localhost:9443/api/health-check/v1.0/health)" = "200" ] \
    || { echo "IS did not start. See $carbon_home/repository/logs/wso2carbon.log" >&2; exit 1; }

# CDS checks the host name of IS. A wrong certificate here makes all CDS calls
# to IS fail later, so stop now.
subject="$(openssl s_client -connect 127.0.0.1:9443 -servername "$is_hostname" </dev/null 2>/dev/null \
    | openssl x509 -noout -ext subjectAltName 2>/dev/null || true)"
if ! printf '%s' "$subject" | grep -q "DNS:$is_hostname"; then
    echo "IS does not serve the test certificate for $is_hostname. Check [keystore.tls]." >&2
    exit 1
fi
if grep -qi 'ERROR.*customer.data.service' "$carbon_home/repository/logs/wso2carbon.log"; then
    echo "WARNING: the IS log has errors for the CDS bundles." >&2
fi
if [ -n "$cluster_members" ]; then
    # The other node can start later, so a missing member is only a warning.
    if grep -q 'Member joined' "$carbon_home/repository/logs/wso2carbon.log"; then
        echo "The IS cluster has more than one member."
    else
        echo "No other IS cluster member joined yet. This is normal for the first node."
    fi
fi
echo "IS is up on https://$is_hostname:9443 with the test certificate."
