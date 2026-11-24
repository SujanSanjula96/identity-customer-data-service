#!/usr/bin/env bash
# Registers in the Identity Server (IS) the items that CDS and the load client
# need. Runs on the bastion node, after setup-is.sh. The steps are the same as
# provision_is in scripts/local-setup/script.sh, without the Console steps.
#
#   provision-is.sh provision   the CDS profile claim, the system application
#                               for CDS (only for an IS without the CDS
#                               integration), and the client application for JMeter.
#                               Writes ~/cds-perf/state.env (mode 600).
#   provision-is.sh enable      enables CDS for the organization, as the Console
#                               does: first in IS (PATCH /api/server/v1/configs/cds,
#                               if IS has this API), then cds_enabled and the
#                               system applications through the CDS load
#                               balancer. Run it after CDS is up.

set -euo pipefail

bundle_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
state_file="$bundle_dir/state.env"
# IS_BASE_URL and CDS_BASE_URL are for a local check only. The AWS run uses
# the defaults (the names of nginx).
is_base="${IS_BASE_URL:-https://wso2is:9443}"
cds_base="${CDS_BASE_URL:-https://cdslb}"
tenant="carbon.super"
audience="iam-cds"
token_expiry=7200
sys_app_name="CDS System App"
client_app_name="CDS Perf Client App"
mgmt_api="$is_base/api/server/v1"

# shellcheck source=/dev/null
source "$bundle_dir/secrets.env"
: "${IS_ADMIN_PASS:?}"

cds_api_identifiers="/cds/api/v1/profiles
/cds/api/v1/profile-schema
/cds/api/v1/unification-rules
/cds/api/v1/config
/cds/api/v1/organizations
/cds/api/v1/identity-resolution
/cds/api/v1/consent-categories"

die() { echo "ERROR: $*" >&2; exit 1; }
ok() { echo "  ok: $*"; }

# The test CA signed the IS certificate, so curl checks it.
isapi() { curl -s --cacert "$bundle_dir/certs/ca.crt" --max-time 60 -u "admin:${IS_ADMIN_PASS}" "$@"; }
urlencode() { jq -rn --arg v "$1" '$v|@uri'; }

state_set() {
    touch "$state_file"
    chmod 600 "$state_file"
    grep -v "^$1=" "$state_file" >"$state_file.tmp" || true
    printf '%s=%s\n' "$1" "$2" >>"$state_file.tmp"
    mv "$state_file.tmp" "$state_file"
    chmod 600 "$state_file"
}
state_get() { grep "^$1=" "$state_file" 2>/dev/null | tail -1 | cut -d= -f2-; }

api_resource_id() {
    isapi "$mgmt_api/api-resources?filter=identifier+eq+$(urlencode "$1")" | jq -r '.apiResources[0].id // empty'
}
api_resource_scopes() { isapi "$mgmt_api/api-resources/$1/scopes" | jq -r '.[]?.name'; }
app_id_by_name() {
    isapi "$mgmt_api/applications?filter=name+eq+$(urlencode "$1")" | jq -r '.applications[0].id // empty'
}
oidc_config() { isapi "$mgmt_api/applications/$1/inbound-protocols/oidc"; }

create_m2m_app() {
    local body
    body="$(jq -n --arg n "$1" --argjson exp "$token_expiry" '{
      name: $n,
      description: "Created by the CDS performance test setup",
      isManagementApp: true,
      inboundProtocolConfiguration: { oidc: {
        grantTypes: ["client_credentials"],
        accessToken: { type: "JWT", userAccessTokenExpiryInSeconds: $exp, applicationAccessTokenExpiryInSeconds: $exp }
      }}
    }')"
    isapi -X POST "$mgmt_api/applications" -H 'Content-Type: application/json' -d "$body" -o /dev/null
    app_id_by_name "$1"
}

authorize_api() {
    local app_id="$1" identifier="$2" res_id scopes_json present
    shift 2
    res_id="$(api_resource_id "$identifier")"
    [ -n "$res_id" ] || return 1
    scopes_json="$(printf '%s\n' "$@" | jq -R . | jq -s .)"
    present="$(isapi "$mgmt_api/applications/$app_id/authorized-apis" | jq -r --arg id "$res_id" '.[]?|select(.id==$id)|.id')"
    if [ -z "$present" ]; then
        isapi -X POST "$mgmt_api/applications/$app_id/authorized-apis" -H 'Content-Type: application/json' \
            -d "$(jq -n --arg id "$res_id" --argjson s "$scopes_json" '{id:$id, policyIdentifier:"RBAC", scopes:$s}')" -o /dev/null
    else
        isapi -X PATCH "$mgmt_api/applications/$app_id/authorized-apis/$res_id" -H 'Content-Type: application/json' \
            -d "$(jq -n --argjson s "$scopes_json" '{addedScopes:$s, removedScopes:[]}')" -o /dev/null
    fi
}

set_token_audience() {
    local current patched
    current="$(oidc_config "$1")"
    echo "$current" | jq -e --arg a "$audience" '.idToken.audience // [] | index($a)' >/dev/null 2>&1 && return 0
    patched="$(echo "$current" | jq --arg a "$audience" '.idToken.audience = ((.idToken.audience // []) + [$a] | unique)')"
    isapi -X PUT "$mgmt_api/applications/$1/inbound-protocols/oidc" -H 'Content-Type: application/json' \
        -d "$patched" -o /dev/null
}

provision_claim() {
    local uri="http://wso2.org/claims/cdsProfile" dialect_id scim_uri="urn:scim:wso2:schema:cdsProfile"
    if [ -z "$(isapi "$mgmt_api/claim-dialects/local/claims" | jq -r --arg u "$uri" '.[]?|select(.claimURI==$u)|.id')" ]; then
        isapi -X POST "$mgmt_api/claim-dialects/local/claims" -H 'Content-Type: application/json' -o /dev/null \
            -d "$(jq -n --arg u "$uri" '{claimURI:$u, description:"Customer Data Service profile identifier",
                 displayName:"CDS Profile", displayOrder:0, readOnly:false, required:false, supportedByDefault:false,
                 attributeMapping:[{mappedAttribute:"cdsProfile", userstore:"PRIMARY"}], properties:[]}')"
    fi
    ok "local claim $uri"
    dialect_id="$(isapi "$mgmt_api/claim-dialects" \
        | jq -r '(if type=="array" then . else .dialects end)[]?|select(.dialectURI=="urn:scim:wso2:schema")|.id')"
    [ -n "$dialect_id" ] || { echo "  warn: no urn:scim:wso2:schema dialect"; return 0; }
    if [ -z "$(isapi "$mgmt_api/claim-dialects/$dialect_id/claims" | jq -r --arg u "$scim_uri" '.[]?|select(.claimURI==$u)|.id')" ]; then
        isapi -X POST "$mgmt_api/claim-dialects/$dialect_id/claims" -H 'Content-Type: application/json' -o /dev/null \
            -d "$(jq -n --arg u "$scim_uri" --arg l "$uri" '{claimURI:$u, mappedLocalClaimURI:$l}')"
    fi
    ok "SCIM2 claim $scim_uri"
}

cmd_provision() {
    local ident missing="" sys_id cli_id scopes
    echo "==> Provision IS at $is_base"
    for ident in $cds_api_identifiers; do
        [ -n "$(api_resource_id "$ident")" ] || missing="$missing $ident"
    done
    [ -z "$missing" ] || die "IS did not register the CDS API resources:$missing. Check the CDS section in deployment.toml."
    ok "CDS API resources"

    provision_claim

    # An IS with the CDS integration (it has GET /api/server/v1/configs/cds)
    # makes the CDS application of each organization itself, when the enable
    # step turns CDS on, and sends its credentials to CDS (service
    # credentials). Then CDS needs no configured client, and this script makes
    # no system application. An older IS needs it.
    if [ "$(isapi -o /dev/null -w '%{http_code}' "$mgmt_api/configs/cds")" = "200" ]; then
        ok "IS has the CDS integration. No '$sys_app_name': CDS uses the service credentials from IS."
    else
        sys_id="$(app_id_by_name "$sys_app_name")"
        [ -n "$sys_id" ] || sys_id="$(create_m2m_app "$sys_app_name")"
        [ -n "$sys_id" ] || die "Cannot create '$sys_app_name'."
        authorize_api "$sys_id" "/api/server/v1/applications" internal_application_mgt_view || die "No /api/server/v1/applications resource."
        authorize_api "$sys_id" "/api/server/v1/claim-dialects" internal_claim_meta_view || die "No /api/server/v1/claim-dialects resource."
        authorize_api "$sys_id" "/scim2/Users" internal_user_mgt_list internal_user_mgt_view || die "No /scim2/Users resource."
        authorize_api "$sys_id" "/api/server/v1/organizations" internal_organization_view || die "No /api/server/v1/organizations resource."
        state_set SYS_CLIENT_ID "$(oidc_config "$sys_id" | jq -r '.clientId // empty')"
        state_set SYS_CLIENT_SECRET "$(oidc_config "$sys_id" | jq -r '.clientSecret // empty')"
        [ -n "$(state_get SYS_CLIENT_ID)" ] || die "No client ID for '$sys_app_name'."
        ok "'$sys_app_name'"
    fi

    cli_id="$(app_id_by_name "$client_app_name")"
    [ -n "$cli_id" ] || cli_id="$(create_m2m_app "$client_app_name")"
    [ -n "$cli_id" ] || die "Cannot create '$client_app_name'."
    set_token_audience "$cli_id"
    for ident in $cds_api_identifiers; do
        scopes="$(api_resource_scopes "$(api_resource_id "$ident")" | tr '\n' ' ')"
        [ -n "$scopes" ] || die "No scopes on $ident."
        # shellcheck disable=SC2086  # One argument for each scope.
        authorize_api "$cli_id" "$ident" $scopes || die "Cannot authorize $ident."
    done
    # The enable step calls the CDS configuration API of IS. With it, IS makes
    # the service credentials of the organization and sends them to CDS.
    authorize_api "$cli_id" "/api/server/v1/configs" internal_config_view internal_config_update \
        || die "Cannot authorize /api/server/v1/configs."
    state_set CLIENT_APP_ID "$cli_id"
    state_set CLIENT_ID "$(oidc_config "$cli_id" | jq -r '.clientId // empty')"
    state_set CLIENT_SECRET "$(oidc_config "$cli_id" | jq -r '.clientSecret // empty')"
    state_set CONSOLE_APP_ID "$(app_id_by_name Console)"
    ok "'$client_app_name' with audience '$audience' (state in $state_file)"
}

cmd_enable() {
    local client_id client_secret scopes token body code
    client_id="$(state_get CLIENT_ID)"
    client_secret="$(state_get CLIENT_SECRET)"
    [ -n "$client_id" ] || die "No CLIENT_ID in $state_file. Run '$0 provision' first."
    scopes="internal_cds_admin_config_view internal_cds_admin_config_update internal_config_view internal_config_update"
    token="$(curl -s --cacert "$bundle_dir/certs/ca.crt" --max-time 30 -u "$client_id:$client_secret" \
        "$is_base/t/$tenant/oauth2/token" -d grant_type=client_credentials --data-urlencode "scope=$scopes" \
        | jq -r '.access_token // empty')"
    [ -n "$token" ] || die "No token from IS for the client application."

    # 1. IS. An IS with the CDS integration has this API. It makes an application for CDS in the
    #    organization and sends its credentials to CDS (service credentials).
    echo "==> Enable CDS for $tenant in IS"
    code="$(curl -s --cacert "$bundle_dir/certs/ca.crt" --max-time 30 -o /tmp/is-cds-config.json -w '%{http_code}' \
        -H "Authorization: Bearer $token" "$mgmt_api/configs/cds")"
    case "$code" in
        200)
            if [ "$(jq -r '.enabled // false' /tmp/is-cds-config.json)" = "true" ]; then
                ok "CDS is enabled in IS for $tenant"
            else
                code="$(curl -s --cacert "$bundle_dir/certs/ca.crt" --max-time 120 -o /tmp/is-cds-config.json \
                    -w '%{http_code}' -X PATCH -H "Authorization: Bearer $token" -H 'Content-Type: application/json' \
                    -d '{"enabled": true}' "$mgmt_api/configs/cds")"
                case "$code" in
                    200 | 204) ok "CDS enabled in IS for $tenant" ;;
                    *) die "IS cannot enable CDS (HTTP $code): $(cat /tmp/is-cds-config.json). Check the IS log." ;;
                esac
            fi
            ;;
        404) echo "  IS has no CDS configuration API. Only CDS is changed." ;;
        *) die "Cannot read the CDS configuration of IS (HTTP $code): $(cat /tmp/is-cds-config.json)" ;;
    esac

    # 2. CDS: cds_enabled and the system applications (the Console sends the
    #    same request after step 1). The first enable runs the schema sync.
    echo "==> Enable CDS for $tenant through $cds_base"
    body="$(jq -n --arg console "$(state_get CONSOLE_APP_ID)" --arg cli "$(state_get CLIENT_APP_ID)" \
        '{cds_enabled: true, system_applications: ([$console, $cli] | map(select(. != "")))}')"
    code="$(curl -s --cacert "$bundle_dir/certs/ca.crt" --max-time 120 -o /tmp/enable-cds.json -w '%{http_code}' \
        -X PATCH -H "Authorization: Bearer $token" -H 'Content-Type: application/json' \
        -d "$body" "$cds_base/t/$tenant/cds/api/v1/config")"
    case "$code" in
        200 | 204) ok "cds_enabled = true for $tenant (schema sync done)" ;;
        *) die "Cannot enable CDS (HTTP $code): $(cat /tmp/enable-cds.json). Check the CDS log on the CDS nodes." ;;
    esac
}

case "${1:-}" in
    provision) cmd_provision ;;
    enable) cmd_enable ;;
    *) echo "Usage: $0 provision|enable" >&2; exit 2 ;;
esac
