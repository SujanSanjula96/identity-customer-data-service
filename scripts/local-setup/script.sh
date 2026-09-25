#!/usr/bin/env bash
#
# Sets up CDS with a WSO2 Identity Server for local development: builds CDS and
# the IS extension bundles, generates and exchanges TLS certificates, writes
# both configurations, registers the OAuth applications CDS needs, starts both
# servers and runs smoke tests.
#
#   scripts/local-setup/script.sh up [options]     provision and start
#   scripts/local-setup/script.sh start            start without provisioning
#   scripts/local-setup/script.sh restart          stop, then start
#   scripts/local-setup/script.sh down [options]   stop
#   scripts/local-setup/script.sh status
#   scripts/local-setup/script.sh logs [cds|is]
#
# `up` is idempotent. After it has succeeded once for a work directory, `start`
# reuses the pack, applications and configuration it left behind.
#
# Templates for the generated configuration are in templates/. See
# docs/guides/local-development.md.
#
set -euo pipefail

# --------------------------------------------------------------------------- #
# Defaults
# --------------------------------------------------------------------------- #
SELF_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_DIR="$(cd "$SELF_DIR/../.." && pwd)"
TPL_DIR="$SELF_DIR/templates"

CMD="up"
DB="sqlite"
WORK_DIR=""
IS_ZIP=""
IS_SRC=""
IS_REF="master"
IS_REBUILD="0"
EXT_SRC=""
EXT_JARS=""

IS_HOST="localhost"
IS_PORT=""
IS_OFFSET="0"
IS_ADMIN_USER="admin"
IS_ADMIN_PASS="admin"
TENANT="carbon.super"

CDS_LISTEN_HOST="127.0.0.1"
CDS_HOST="localhost"
CDS_PORT="8900"
CDS_LOG_LEVEL="DEBUG"
# Basic-auth credentials for the IS -> CDS sync endpoints; both sides must
# match. The password is generated per work directory - see resolve_secrets.
CDS_SYNC_USER="admin"
CDS_SYNC_PASS=""
AUDIENCE="iam-cds"
TOKEN_EXPIRY="3600"

PG_IMAGE="postgres:16"
PG_CONTAINER="cds-postgres"
PG_HOST="localhost"
PG_PORT="5432"
PG_USER="cds"
PG_PASS=""
PG_DB="cds_db"
PG_EXTERNAL="0"

CLEAN="0"
FORCE="0"
PURGE="0"
SKIP_TESTS="0"
WITH_TESTS="0"
SKIP_IS="0"
SKIP_CDS="0"
KEEP_TEST_USER="0"

IS_REPO="https://github.com/wso2/product-is.git"
EXT_REPO="https://github.com/wso2-extensions/identity-customer-data-service-extensions.git"

SYS_APP_NAME="CDS System App"
CLIENT_APP_NAME="CDS Client App"
# Markers around the generated deployment.toml section. A re-run matches on the
# prefixes, so the rest of the marker text can change without stranding a block
# written by an earlier version.
TOML_BEGIN_MARK="# BEGIN cds-local-dev"
TOML_END_MARK="# END cds-local-dev"
TOML_BEGIN="$TOML_BEGIN_MARK (generated from templates/is-deployment.toml - do not edit)"
TOML_END="$TOML_END_MARK"

# --------------------------------------------------------------------------- #
# Output helpers
# --------------------------------------------------------------------------- #
if [ -t 1 ]; then
  C_RESET="$(printf '\033[0m')"; C_BOLD="$(printf '\033[1m')"
  C_RED="$(printf '\033[31m')"; C_GREEN="$(printf '\033[32m')"
  C_YELLOW="$(printf '\033[33m')"; C_BLUE="$(printf '\033[34m')"
else
  C_RESET=""; C_BOLD=""; C_RED=""; C_GREEN=""; C_YELLOW=""; C_BLUE=""
fi

step() { printf '\n%s==> %s%s\n' "${C_BOLD}${C_BLUE}" "$*" "${C_RESET}"; }
info() { printf '    %s\n' "$*"; }
ok()   { printf '    %s+%s %s\n' "${C_GREEN}" "${C_RESET}" "$*"; }
warn() { printf '    %s!%s %s\n' "${C_YELLOW}" "${C_RESET}" "$*" >&2; }
die()  { printf '\n%sERROR:%s %s\n' "${C_RED}${C_BOLD}" "${C_RESET}" "$*" >&2; exit 1; }

usage() {
  awk 'NR>2 && /^#/ {sub(/^# ?/, ""); print; next} NR>2 && !/^#/ {exit}' "${BASH_SOURCE[0]}"
  cat <<'USAGE'

Options:
  --db sqlite|postgres         CDS datasource (default: sqlite)
  --work-dir PATH              runtime directory (default: <repo>/.local-dev)
  --is-zip PATH                local IS pack zip (skips building one)
  --is-src PATH                existing product-is checkout to build
  --is-ref REF                 product-is branch or tag to build (default: master)
  --is-rebuild                 rebuild the pack even if one was built before
  --extensions-src PATH        existing clone of the IS extensions repo
  --extensions-jars PATH       directory containing the four extension jars
  --is-host HOST               IS hostname (default: localhost)
  --is-port PORT               IS HTTPS port (default: 9443 + offset)
  --is-offset N                IS port offset, to run beside another IS (default: 0)
  --is-admin-user USER         IS admin username (default: admin)
  --is-admin-pass PASS         IS admin password (default: admin)
  --tenant TENANT              org handle (default and only supported: carbon.super)
  --cds-host HOST              hostname the Console uses for CDS (default: localhost)
  --cds-port PORT              CDS HTTPS port (default: 8900)
  --cds-log-level LEVEL        CDS log level (default: DEBUG)
  --sync-user USER             IS<->CDS sync Basic-auth user (default: admin)
  --sync-pass PASS             IS<->CDS sync Basic-auth password (default: generated)
  --pg-image IMAGE             Postgres docker image (default: postgres:16)
  --pg-container NAME          Postgres container name (default: cds-postgres)
  --pg-host/--pg-port/--pg-user/--pg-pass/--pg-db
                               Postgres connection settings (password: generated)
  --pg-external                do not manage a container; use an existing server
  --clean                      re-extract the IS pack and re-create the CDS home
  --force                      kill processes already holding the ports
  --skip-is                    leave IS alone (CDS only)
  --skip-cds                   leave CDS alone (IS only)
  --skip-tests                 skip the smoke tests
  --tests                      (start) run the smoke tests too
  --keep-test-user             keep the user created by the sync smoke test
  --purge                      (down) delete the work directory too
  -h, --help                   this help

start, restart, down, status and logs reuse the settings recorded by the last
successful `up` in the work directory, so they need only --work-dir, and not
even that for the default one.
USAGE
}

# --------------------------------------------------------------------------- #
# Run state - $WORK_DIR/state.env
# --------------------------------------------------------------------------- #
# state_get KEY -> value from $STATE_FILE (empty if absent)
state_get() {
  [ -f "$STATE_FILE" ] || return 0
  sed -n "s/^$1=//p" "$STATE_FILE" | tail -1
}

# state_set KEY VALUE
state_set() {
  local key="$1" val="$2" tmp
  mkdir -p "$(dirname "$STATE_FILE")"
  touch "$STATE_FILE"
  tmp="$STATE_FILE.tmp"
  grep -v "^$key=" "$STATE_FILE" > "$tmp" 2>/dev/null || :
  printf '%s=%s\n' "$key" "$val" >> "$tmp"
  mv "$tmp" "$STATE_FILE"
  chmod 600 "$STATE_FILE"
}

# Settings recorded by `up` so that later commands do not need the flags again.
# VAR:flag - passing the flag skips the restore for that value.
RUN_SETTINGS="DB:--db
IS_HOST:--is-host IS_PORT:--is-port IS_OFFSET:--is-offset
IS_ADMIN_USER:--is-admin-user IS_ADMIN_PASS:--is-admin-pass TENANT:--tenant
CDS_HOST:--cds-host CDS_PORT:--cds-port CDS_LOG_LEVEL:--cds-log-level
CDS_SYNC_USER:--sync-user CDS_SYNC_PASS:--sync-pass
PG_IMAGE:--pg-image PG_CONTAINER:--pg-container PG_HOST:--pg-host
PG_PORT:--pg-port PG_USER:--pg-user PG_PASS:--pg-pass PG_DB:--pg-db
PG_EXTERNAL:--pg-external"

save_run_settings() {
  local pair
  for pair in $RUN_SETTINGS; do
    eval "state_set \"${pair%%:*}\" \"\${${pair%%:*}}\""
  done
}

restore_run_settings() {
  local pair var val
  for pair in $RUN_SETTINGS; do
    var="${pair%%:*}"
    arg_given "${pair##*:}" && continue
    val="$(state_get "$var")"
    [ -n "$val" ] && eval "$var=\$val"
  done
  return 0
}

# --------------------------------------------------------------------------- #
# Argument parsing
# --------------------------------------------------------------------------- #
case "${1:-}" in
  up|start|restart|down|status|logs) CMD="$1"; shift ;;
  -h|--help) usage; exit 0 ;;
  ""|-*) ;;   # no command, or options only: CMD stays at its default
  *) die "unknown command: $1 (expected up, start, restart, down, status or logs)" ;;
esac

LOGS_TARGET=""
if [ "$CMD" = "logs" ] && [ $# -gt 0 ]; then
  case "$1" in cds|is) LOGS_TARGET="$1"; shift ;; esac
fi

need_val() { [ $# -ge 2 ] || die "$1 requires a value"; }

# The options given on this invocation - see restore_run_settings.
ARGS_RAW=" $* "
arg_given() {
  case "$ARGS_RAW" in
    *" $1 "*|*" $1="*) return 0 ;;
  esac
  return 1
}

while [ $# -gt 0 ]; do
  case "$1" in
    --db) need_val "$@"; DB="$2"; shift 2 ;;
    --work-dir) need_val "$@"; WORK_DIR="$2"; shift 2 ;;
    --is-zip) need_val "$@"; IS_ZIP="$2"; shift 2 ;;
    --is-src) need_val "$@"; IS_SRC="$2"; shift 2 ;;
    --is-ref) need_val "$@"; IS_REF="$2"; shift 2 ;;
    --is-rebuild) IS_REBUILD="1"; shift ;;
    --extensions-src) need_val "$@"; EXT_SRC="$2"; shift 2 ;;
    --extensions-jars) need_val "$@"; EXT_JARS="$2"; shift 2 ;;
    --is-host) need_val "$@"; IS_HOST="$2"; shift 2 ;;
    --is-port) need_val "$@"; IS_PORT="$2"; shift 2 ;;
    --is-offset) need_val "$@"; IS_OFFSET="$2"; shift 2 ;;
    --is-admin-user) need_val "$@"; IS_ADMIN_USER="$2"; shift 2 ;;
    --is-admin-pass) need_val "$@"; IS_ADMIN_PASS="$2"; shift 2 ;;
    --tenant) need_val "$@"; TENANT="$2"; shift 2 ;;
    --cds-host) need_val "$@"; CDS_HOST="$2"; shift 2 ;;
    --cds-port) need_val "$@"; CDS_PORT="$2"; shift 2 ;;
    --cds-log-level) need_val "$@"; CDS_LOG_LEVEL="$2"; shift 2 ;;
    --sync-user) need_val "$@"; CDS_SYNC_USER="$2"; shift 2 ;;
    --sync-pass) need_val "$@"; CDS_SYNC_PASS="$2"; shift 2 ;;
    --pg-image) need_val "$@"; PG_IMAGE="$2"; shift 2 ;;
    --pg-container) need_val "$@"; PG_CONTAINER="$2"; shift 2 ;;
    --pg-host) need_val "$@"; PG_HOST="$2"; shift 2 ;;
    --pg-port) need_val "$@"; PG_PORT="$2"; shift 2 ;;
    --pg-user) need_val "$@"; PG_USER="$2"; shift 2 ;;
    --pg-pass) need_val "$@"; PG_PASS="$2"; shift 2 ;;
    --pg-db) need_val "$@"; PG_DB="$2"; shift 2 ;;
    --pg-external) PG_EXTERNAL="1"; shift ;;
    --clean) CLEAN="1"; shift ;;
    --force) FORCE="1"; shift ;;
    --purge) PURGE="1"; shift ;;
    --skip-is) SKIP_IS="1"; shift ;;
    --skip-cds) SKIP_CDS="1"; shift ;;
    --skip-tests) SKIP_TESTS="1"; shift ;;
    --tests) WITH_TESTS="1"; shift ;;
    --keep-test-user) KEEP_TEST_USER="1"; shift ;;
    -h|--help) usage; exit 0 ;;
    *) die "unknown option: $1 (try --help)" ;;
  esac
done

[ -n "$WORK_DIR" ] || WORK_DIR="$REPO_DIR/.local-dev"
mkdir -p "$WORK_DIR"
WORK_DIR="$(cd "$WORK_DIR" && pwd)"

IS_ROOT="$WORK_DIR/is"
CDS_HOME="$WORK_DIR/cds-home"
CERT_DIR="$CDS_HOME/etc/certs"
BIN_DIR="$WORK_DIR/bin"
SRC_DIR="$WORK_DIR/src"
LOG_DIR="$WORK_DIR/logs"
RUN_DIR="$WORK_DIR/run"
STATE_FILE="$WORK_DIR/state.env"

# Commands other than `up` take their settings from the state file; options on
# the command line still win.
if [ "$CMD" != "up" ]; then restore_run_settings; fi

case "$IS_OFFSET" in
  ""|*[!0-9]*) die "--is-offset must be a non-negative integer (got '$IS_OFFSET')" ;;
esac
IS_HTTP_PORT=$((9763 + IS_OFFSET))
[ -n "$IS_PORT" ] || IS_PORT=$((9443 + IS_OFFSET))

case "$DB" in
  sqlite|postgres) ;;
  *) die "--db must be sqlite or postgres (got '$DB')" ;;
esac

IS_BASE="https://$IS_HOST:$IS_PORT"
CDS_BASE="https://$CDS_HOST:$CDS_PORT"
CDS_URL_LOCAL="https://$CDS_LISTEN_HOST:$CDS_PORT"
MGMT_API="$IS_BASE/api/server/v1"

# --------------------------------------------------------------------------- #
# Generic helpers
# --------------------------------------------------------------------------- #
# gen_secret - a random secret, safe to use unquoted in TOML, YAML and a DSN.
gen_secret() { openssl rand -hex 16; }

# toml_str VALUE - escape a value for a TOML basic string.
toml_str() { printf '%s' "$1" | sed -e 's/\\/\\\\/g' -e 's/"/\\"/g'; }

# yaml_block FILE KEY - a two-space-indented block and everything under it.
yaml_block() {
  awk -v key="  $2:" '
    $0 == key             { inside = 1; print; next }
    inside && /^[^ ]/     { inside = 0 }
    inside && /^  [^ ]/   { inside = 0 }
    inside                { print }
  ' "$1"
}

# render TEMPLATE NAME=VALUE ... -> the filled-in template on stdout.
# ${NAME} becomes its value; a placeholder with no value is an error. Values are
# passed through the environment, so none of them needs escaping.
render() {
  local tpl="$1"; shift
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
  ) || die "could not render $(basename "$tpl")"
}

require_bins() {
  local missing="" b
  for b in "$@"; do
    command -v "$b" >/dev/null 2>&1 || missing="$missing $b"
  done
  [ -z "$missing" ] || die "missing required tool(s):$missing"
}

# resolve_java - export a JAVA_HOME the Identity Server supports. An exported
# JAVA_HOME wins; otherwise prefer an installed JDK 21 over the machine default.
JAVA_RESOLVED="0"
resolve_java() {
  if [ "$JAVA_RESOLVED" = "1" ]; then return 0; fi
  if [ -z "${JAVA_HOME:-}" ] && [ -x /usr/libexec/java_home ]; then
    JAVA_HOME="$(/usr/libexec/java_home -v 21 2>/dev/null || :)"
    [ -n "$JAVA_HOME" ] || JAVA_HOME="$(/usr/libexec/java_home 2>/dev/null || :)"
  fi
  [ -n "${JAVA_HOME:-}" ] || die "JAVA_HOME is not set and could not be detected"
  export JAVA_HOME
  local major
  # Java 8 and older report the feature number as 1.x.
  major="$("$JAVA_HOME/bin/java" -version 2>&1 \
    | sed -n '1s/.*version "\(1\.\)\{0,1\}\([0-9][0-9]*\).*/\2/p')"
  info "JAVA_HOME=$JAVA_HOME (Java ${major:-unknown})"
  case "$major" in
    ''|*[!0-9]*) ;;
    *)
      if [ "$major" -lt 21 ]; then
        die "the Identity Server needs Java 21; $JAVA_HOME is Java $major.
product-is builds with source and target 21, so an older JDK cannot compile it.
Install a JDK 21, or point JAVA_HOME at one."
      elif [ "$major" -gt 21 ]; then
        warn "the Identity Server is built and tested on Java 21; Java $major may fail"
      fi
      ;;
  esac
  JAVA_RESOLVED="1"
}

port_pids() { lsof -nP -iTCP:"$1" -sTCP:LISTEN -t 2>/dev/null || :; }

port_free() { [ -z "$(port_pids "$1")" ]; }

# ensure_port_free PORT LABEL
ensure_port_free() {
  local port="$1" label="$2" pids
  pids="$(port_pids "$port")"
  [ -n "$pids" ] || return 0
  if [ "$FORCE" = "1" ]; then
    warn "port $port ($label) in use by PID(s) $(echo $pids | tr '\n' ' ')- killing (--force)"
    for p in $pids; do kill "$p" 2>/dev/null || :; done
    sleep 3
    pids="$(port_pids "$port")"
    if [ -n "$pids" ]; then
      for p in $pids; do kill -9 "$p" 2>/dev/null || :; done
      sleep 2
    fi
    port_free "$port" || die "port $port ($label) still in use after --force"
  else
    die "port $port is already in use by PID(s) $(echo $pids | tr '\n' ' ')($label). Stop it, or re-run with --force."
  fi
}

# wait_http URL TIMEOUT_SECONDS LABEL [EXPECTED_CODE]
wait_http() {
  local url="$1" timeout="$2" label="$3" expect="${4:-200}"
  local waited=0 code
  printf '    waiting for %s ' "$label"
  while [ "$waited" -lt "$timeout" ]; do
    code="$(curl -sk -o /dev/null -w '%{http_code}' --max-time 5 "$url" 2>/dev/null || echo 000)"
    if [ "$code" = "$expect" ]; then
      printf ' %sup%s (%ss)\n' "${C_GREEN}" "${C_RESET}" "$waited"
      return 0
    fi
    printf '.'
    sleep 3
    waited=$((waited + 3))
  done
  printf ' %stimed out%s\n' "${C_RED}" "${C_RESET}"
  return 1
}

# read_pid PIDFILE - the IS pid file is NUL-padded, so keep only the digits.
read_pid() {
  [ -f "$1" ] || return 1
  local pid
  pid="$(tr -cd '0-9' < "$1" 2>/dev/null | cut -c1-12)"
  [ -n "$pid" ] || return 1
  printf '%s' "$pid"
}

# is_running PIDFILE
is_running() {
  local pid
  pid="$(read_pid "$1")" || return 1
  kill -0 "$pid" 2>/dev/null
}

# stop_pid PID LABEL TIMEOUT - TERM, wait, then KILL. Returns non-zero if it survives.
stop_pid() {
  local pid="$1" label="$2" timeout="${3:-90}" waited=0
  kill -0 "$pid" 2>/dev/null || return 0
  kill -TERM "$pid" 2>/dev/null || :
  while [ "$waited" -lt "$timeout" ]; do
    kill -0 "$pid" 2>/dev/null || return 0
    sleep 2; waited=$((waited + 2))
  done
  warn "$label (pid $pid) ignored SIGTERM after ${timeout}s - sending SIGKILL"
  kill -9 "$pid" 2>/dev/null || :
  sleep 2
  kill -0 "$pid" 2>/dev/null && return 1
  return 0
}

urlencode() { jq -rn --arg v "$1" '$v|@uri'; }

# IS management API call as admin
isapi() { curl -sk --max-time 60 -u "${IS_ADMIN_USER}:${IS_ADMIN_PASS}" "$@"; }


# --------------------------------------------------------------------------- #
# Path resolution for the IS pack
# --------------------------------------------------------------------------- #
resolve_is_home() {
  local d
  for d in "$IS_ROOT"/wso2is-*; do
    if [ -d "$d" ] && [ -x "$d/bin/wso2server.sh" ]; then echo "$d"; return 0; fi
  done
  return 1
}

# --------------------------------------------------------------------------- #
# 1. Preflight
# --------------------------------------------------------------------------- #
preflight() {
  step "Preflight"
  require_bins curl jq unzip openssl git lsof
  [ "$SKIP_CDS" = "1" ] || require_bins go
  if [ "$SKIP_IS" != "1" ]; then
    require_bins java keytool
    resolve_java
    # Maven builds the extension bundles, and the IS pack unless one is supplied.
    if [ -z "$EXT_JARS" ] || [ -z "$IS_ZIP" ]; then
      require_bins mvn
    fi
  fi
  if [ "$DB" = "postgres" ]; then
    if [ "$PG_EXTERNAL" = "1" ]; then require_bins psql; else require_bins docker; fi
  fi
  [ -f "$REPO_DIR/go.mod" ] || die "$REPO_DIR does not look like the CDS repo (no go.mod)"

  # The templates and helpers next to this script are required.
  local asset missing=""
  for asset in templates/is-deployment.toml templates/cds-deployment.yaml \
               templates/datasource-sqlite.yaml templates/datasource-postgres.yaml \
               templates/openssl.cnf; do
    [ -f "$SELF_DIR/$asset" ] || missing="$missing $asset"
  done
  [ -z "$missing" ] || die "missing files under $SELF_DIR:$missing"

  if [ "$SKIP_CDS" != "1" ] && ! is_running "$RUN_DIR/cds.pid"; then
    ensure_port_free "$CDS_PORT" "CDS"
  fi
  if [ "$SKIP_IS" != "1" ] && ! is_running "$RUN_DIR/is.pid"; then
    ensure_port_free "$IS_PORT" "IS HTTPS"
    ensure_port_free "$IS_HTTP_PORT" "IS HTTP"
  fi
  if [ "$DB" = "postgres" ] && [ "$PG_EXTERNAL" != "1" ]; then
    if ! docker ps --format '{{.Names}}' | grep -qx "$PG_CONTAINER"; then
      ensure_port_free "$PG_PORT" "PostgreSQL"
    fi
  fi

  mkdir -p "$IS_ROOT" "$CERT_DIR" "$BIN_DIR" "$SRC_DIR" "$LOG_DIR" "$RUN_DIR"

  if [ "$CLEAN" = "1" ]; then
    stop_cds
    rm -rf "$CDS_HOME/repository/conf" "$CDS_HOME/repository/database"
    rm -f "$STATE_FILE" "$LOG_DIR/cds.log"
    info "--clean: reset the CDS home and the recorded state"
  fi

  ok "work dir: $WORK_DIR"
  ok "CDS datasource: $DB"
}

# The credentials the script invents rather than reads from IS. Generated once
# per work directory and reused afterwards: a new sync password would leave the
# two sides of the credential out of step under --skip-is, and a new PostgreSQL
# password would not match the container created with the old one. Recorded
# immediately for the same reason - the container outlives a failed run.
resolve_secrets() {
  if ! arg_given --sync-pass; then
    CDS_SYNC_PASS="$(state_get CDS_SYNC_PASS)"
    [ -n "$CDS_SYNC_PASS" ] || CDS_SYNC_PASS="$(gen_secret)"
  fi
  if ! arg_given --pg-pass; then
    PG_PASS="$(state_get PG_PASS)"
    [ -n "$PG_PASS" ] || PG_PASS="$(gen_secret)"
  fi
  state_set CDS_SYNC_PASS "$CDS_SYNC_PASS"
  state_set PG_PASS "$PG_PASS"
}

# --------------------------------------------------------------------------- #
# 2. Build CDS and generate its TLS material
# --------------------------------------------------------------------------- #
build_cds() {
  step "Building CDS"
  ( cd "$REPO_DIR" && go build -o "$BIN_DIR/cds" ./cmd/server ) || die "go build failed"
  ok "built $BIN_DIR/cds ($(cat "$REPO_DIR/version.txt" 2>/dev/null || echo unknown))"
}

generate_cds_cert() {
  step "CDS TLS certificate"
  if [ -f "$CERT_DIR/server.crt" ] && [ -f "$CERT_DIR/server.key" ]; then
    ok "reusing $CERT_DIR/server.crt"
    return 0
  fi
  local cnf="$CERT_DIR/openssl.cnf"
  render "$TPL_DIR/openssl.cnf" "CDS_HOST=$CDS_HOST" > "$cnf"
  openssl req -x509 -newkey rsa:2048 -sha256 -days 825 -nodes \
    -keyout "$CERT_DIR/server.key" -out "$CERT_DIR/server.crt" \
    -config "$cnf" -extensions v3_req >/dev/null 2>&1 \
    || die "failed to generate the CDS self-signed certificate"
  chmod 600 "$CERT_DIR/server.key"
  ok "generated a self-signed certificate for $CDS_HOST (SAN: $CDS_HOST, localhost, 127.0.0.1)"
}

# --------------------------------------------------------------------------- #
# 3. CDS database
# --------------------------------------------------------------------------- #
setup_database() {
  step "CDS database ($DB)"
  if [ "$DB" = "sqlite" ]; then
    ok "SQLite at $CDS_HOME/repository/database/cds.db (CDS creates it and applies dbscripts/sqlite.sql on start)"
    return 0
  fi

  if [ "$PG_EXTERNAL" != "1" ]; then
    if docker ps -a --format '{{.Names}}' | grep -qx "$PG_CONTAINER"; then
      if docker ps --format '{{.Names}}' | grep -qx "$PG_CONTAINER"; then
        ok "container '$PG_CONTAINER' already running"
      else
        docker start "$PG_CONTAINER" >/dev/null || die "failed to start container $PG_CONTAINER"
        ok "started existing container '$PG_CONTAINER'"
      fi
    else
      docker run -d --name "$PG_CONTAINER" \
        -p "$PG_PORT:5432" \
        -e POSTGRES_USER="$PG_USER" -e POSTGRES_PASSWORD="$PG_PASS" -e POSTGRES_DB="$PG_DB" \
        "$PG_IMAGE" >/dev/null || die "failed to start PostgreSQL container"
      state_set CDS_PG_CONTAINER_OWNED "1"
      state_set CDS_PG_CONTAINER "$PG_CONTAINER"
      ok "started $PG_IMAGE as '$PG_CONTAINER' on port $PG_PORT"
    fi
  fi

  local waited=0
  printf '    waiting for PostgreSQL '
  while [ "$waited" -lt 90 ]; do
    if pg_ready; then printf ' %sup%s\n' "${C_GREEN}" "${C_RESET}"; break; fi
    printf '.'; sleep 2; waited=$((waited + 2))
  done
  pg_ready || { printf '\n'; die "PostgreSQL at $PG_HOST:$PG_PORT did not become ready"; }

  # dbscripts/postgres.sql uses bare CREATE TABLE, so apply it only once.
  if [ "$(pg_query "SELECT to_regclass('public.profiles') IS NOT NULL")" = "t" ]; then
    ok "schema already present - skipping dbscripts/postgres.sql"
  else
    pg_apply_file "$REPO_DIR/dbscripts/postgres.sql" || die "failed to apply dbscripts/postgres.sql"
    ok "applied dbscripts/postgres.sql to $PG_DB"
  fi
}

pg_ready() {
  if [ "$PG_EXTERNAL" != "1" ]; then
    docker exec "$PG_CONTAINER" pg_isready -U "$PG_USER" -d "$PG_DB" >/dev/null 2>&1
  else
    PGPASSWORD="$PG_PASS" psql -h "$PG_HOST" -p "$PG_PORT" -U "$PG_USER" -d "$PG_DB" -c 'SELECT 1' >/dev/null 2>&1
  fi
}

pg_query() {
  if [ "$PG_EXTERNAL" != "1" ]; then
    docker exec -e PGPASSWORD="$PG_PASS" "$PG_CONTAINER" \
      psql -qtAX -U "$PG_USER" -d "$PG_DB" -c "$1" 2>/dev/null | tr -d '[:space:]'
  else
    PGPASSWORD="$PG_PASS" psql -qtAX -h "$PG_HOST" -p "$PG_PORT" -U "$PG_USER" -d "$PG_DB" -c "$1" 2>/dev/null | tr -d '[:space:]'
  fi
}

pg_apply_file() {
  if [ "$PG_EXTERNAL" != "1" ]; then
    docker exec -i -e PGPASSWORD="$PG_PASS" "$PG_CONTAINER" \
      psql -q -v ON_ERROR_STOP=1 -U "$PG_USER" -d "$PG_DB" < "$1" >/dev/null
  else
    PGPASSWORD="$PG_PASS" psql -q -v ON_ERROR_STOP=1 -h "$PG_HOST" -p "$PG_PORT" -U "$PG_USER" -d "$PG_DB" -f "$1" >/dev/null
  fi
}

# --------------------------------------------------------------------------- #
# 4. The Identity Server pack
# --------------------------------------------------------------------------- #
# The Console renders the Customer Data section only if its bundled build knows
# the `cds_host` key, so the pack has to be recent:
#
#   --is-zip PATH   an existing pack
#   (default)       built from https://github.com/wso2/product-is
#
IS_PACK_ZIP=""

# find_pack_zip DIR -> the distribution zip in DIR, or empty.
find_pack_zip() {
  { find "$1" -maxdepth 1 -name 'wso2is-*.zip' ! -name '*-src.zip' -type f 2>/dev/null || :; } | head -1
}

# Build the pack from source. Needs github.com and the public WSO2 Maven
# repository only.
build_is_pack() {
  local src="$IS_SRC"
  if [ -n "$src" ]; then
    [ -d "$src" ] || die "--is-src: no such directory: $src"
    src="$(cd "$src" && pwd)"
  else
    src="$SRC_DIR/product-is"
    if [ -d "$src/.git" ]; then
      info "updating the product-is clone ($IS_REF)"
      ( cd "$src" && git fetch -q --depth 1 origin "$IS_REF" && git reset -q --hard FETCH_HEAD ) \
        || warn "could not update the clone - building what is on disk"
    else
      info "cloning $IS_REPO ($IS_REF)"
      git clone -q --depth 1 --branch "$IS_REF" "$IS_REPO" "$src" \
        || die "failed to clone product-is - pass --is-zip PATH to use a pack you already have"
    fi
  fi
  [ -f "$src/modules/distribution/pom.xml" ] || die "$src does not look like a product-is checkout"

  IS_PACK_ZIP="$(find_pack_zip "$src/modules/distribution/target")"
  if [ -n "$IS_PACK_ZIP" ] && [ "$IS_REBUILD" != "1" ]; then
    ok "reusing $(basename "$IS_PACK_ZIP") (--is-rebuild to build it again)"
    return 0
  fi

  resolve_java
  local blog="$LOG_DIR/is-build.log" rc=0 mpid="" name="" last=""
  # Reactor module lines only - maven also logs "Building jar:" for every artifact.
  local modline='^\[INFO\] Building .*\[[0-9][0-9]*/[0-9][0-9]*\]$'
  info "building the pack - 10-30 minutes on a cold ~/.m2, a minute or so warm"
  info "full output: $blog"
  : > "$blog"
  ( cd "$src" && mvn -B clean install -Dmaven.test.skip=true >"$blog" 2>&1 ) &
  mpid=$!
  # An interrupt has to take maven down with the script rather than orphan it.
  trap 'pkill -P "$mpid" 2>/dev/null || :; kill "$mpid" 2>/dev/null || :; exit 130' INT TERM
  while kill -0 "$mpid" 2>/dev/null; do
    name="$(grep -E "$modline" "$blog" 2>/dev/null | tail -1 | sed 's/^\[INFO\] Building //' || true)"
    if [ -t 1 ]; then
      printf '\r    %-74.74s' "$name"
    elif [ "$name" != "$last" ] && [ -n "$name" ]; then
      info "$name"
    fi
    last="$name"
    sleep 5
  done
  wait "$mpid" || rc=$?
  trap - INT TERM
  if [ -t 1 ]; then printf '\r%-78s\r' ''; fi
  [ "$rc" = "0" ] || die "the product-is build failed - see $blog

$(tail -15 "$blog" 2>/dev/null)"

  IS_PACK_ZIP="$(find_pack_zip "$src/modules/distribution/target")"
  [ -n "$IS_PACK_ZIP" ] || die "the build succeeded but no pack appeared in $src/modules/distribution/target"
  ok "built $(basename "$IS_PACK_ZIP") ($(du -h "$IS_PACK_ZIP" | cut -f1))"
}

fetch_is_pack() {
  step "Identity Server pack"

  # An extracted pack is reused as is: a re-run clones and builds nothing.
  local existing
  existing="$(resolve_is_home 2>/dev/null || :)"
  if [ -n "$existing" ] && [ "$CLEAN" = "1" ]; then
    info "--clean: removing $existing"
    rm -rf "$existing"
    existing=""
  fi
  if [ -n "$existing" ]; then
    IS_HOME="$existing"
    ok "reusing extracted pack $(basename "$IS_HOME")"
    state_set IS_HOME "$IS_HOME"
    return 0
  fi

  if [ -n "$IS_ZIP" ]; then
    [ -f "$IS_ZIP" ] || die "--is-zip: no such file: $IS_ZIP"
    IS_PACK_ZIP="$IS_ZIP"
    ok "using local pack $(basename "$IS_PACK_ZIP")"
  else
    build_is_pack
  fi

  info "extracting"
  unzip -q "$IS_PACK_ZIP" -d "$IS_ROOT" || die "failed to extract $IS_PACK_ZIP"
  IS_HOME="$(resolve_is_home)" || die "no wso2is-* directory found after extracting $IS_PACK_ZIP"
  ok "extracted to $IS_HOME"
  state_set IS_HOME "$IS_HOME"
}

# --------------------------------------------------------------------------- #
# 5. IS extension bundles
# --------------------------------------------------------------------------- #
EXT_BUNDLES="org.wso2.identity.customer.data.service.client
org.wso2.identity.customer.data.service.event.handler
org.wso2.identity.customer.data.service.auth.listener
org.wso2.identity.customer.data.service.auth.handler"

deploy_extensions() {
  step "CDS extension bundles"
  local jars_dir="$EXT_JARS" src="$EXT_SRC"

  if [ -z "$jars_dir" ]; then
    if [ -z "$src" ]; then
      src="$SRC_DIR/identity-customer-data-service-extensions"
      if [ -d "$src/.git" ]; then
        info "updating existing clone"
        ( cd "$src" && git fetch -q origin && git reset -q --hard origin/HEAD ) \
          || warn "could not update the extensions clone - building what is on disk"
      else
        info "cloning $EXT_REPO"
        git clone -q "$EXT_REPO" "$src" || die "failed to clone the extensions repo"
      fi
    else
      [ -d "$src" ] || die "--extensions-src: no such directory: $src"
    fi
    resolve_java
    info "building (mvn clean install, checkstyle/spotbugs skipped)"
    ( cd "$src" && mvn -B -q clean install -Dmaven.checkstyle.skip=true -Dspotbugs.skip=true ) \
      || die "extension build failed - see the maven output above"
    jars_dir="$src/components"
  else
    [ -d "$jars_dir" ] || die "--extensions-jars: no such directory: $jars_dir"
  fi

  local dropins="$IS_HOME/repository/components/dropins" b jar count=0
  mkdir -p "$dropins"
  for b in $EXT_BUNDLES; do
    jar="$(find "$jars_dir" -name "$b-*.jar" ! -name '*-sources.jar' ! -name '*-javadoc.jar' -type f 2>/dev/null | head -1)"
    [ -n "$jar" ] || die "extension jar not found for $b under $jars_dir"
    rm -f "$dropins/$b"-*.jar
    cp "$jar" "$dropins/" || die "failed to copy $jar"
    count=$((count + 1))
    info "$(basename "$jar")"
  done
  [ "$count" = "4" ] || die "expected 4 extension jars, deployed $count"
  ok "deployed 4 bundles into repository/components/dropins"
}

# --------------------------------------------------------------------------- #
# 6. Certificate exchange
# --------------------------------------------------------------------------- #
exchange_certificates() {
  step "Certificate exchange"
  local sec="$IS_HOME/repository/resources/security"
  local truststore="$sec/client-truststore.p12"
  local keystore="$sec/wso2carbon.p12"
  local storepass="wso2carbon"
  local alias="cds-$CDS_HOST"

  [ -f "$truststore" ] || die "IS client truststore not found at $truststore"

  # CDS certificate -> IS truststore (IS validates CDS when calling the sync endpoints).
  if keytool -list -keystore "$truststore" -storetype PKCS12 -storepass "$storepass" -alias "$alias" >/dev/null 2>&1; then
    keytool -delete -keystore "$truststore" -storetype PKCS12 -storepass "$storepass" -alias "$alias" >/dev/null 2>&1 || :
  fi
  keytool -importcert -noprompt -trustcacerts -alias "$alias" -file "$CERT_DIR/server.crt" \
    -keystore "$truststore" -storetype PKCS12 -storepass "$storepass" >/dev/null 2>&1 \
    || die "failed to import the CDS certificate into $truststore"
  ok "imported the CDS certificate into client-truststore.p12 (alias $alias)"

  # IS certificate -> CDS trust store (CDS validates IS when calling its APIs).
  [ -f "$keystore" ] || die "IS keystore not found at $keystore"
  local is_alias
  is_alias="$(keytool -list -keystore "$keystore" -storetype PKCS12 -storepass "$storepass" 2>/dev/null \
    | sed -n 's/^\([^,]*\), .*PrivateKeyEntry.*/\1/p' | head -1)"
  [ -n "$is_alias" ] || die "could not find a private key entry in $keystore"
  keytool -exportcert -rfc -alias "$is_alias" -keystore "$keystore" -storetype PKCS12 \
    -storepass "$storepass" > "$CERT_DIR/trust_store.pem" 2>/dev/null \
    || die "failed to export the IS certificate from $keystore"
  [ -s "$CERT_DIR/trust_store.pem" ] || die "exported IS certificate is empty"
  ok "exported the IS certificate (alias $is_alias) to $CERT_DIR/trust_store.pem"
}

# --------------------------------------------------------------------------- #
# 7. deployment.toml
# --------------------------------------------------------------------------- #
# The generated CDS section, in one block between markers so that a re-run
# replaces it instead of appending a second copy.
cds_toml_block() {
  render "$TPL_DIR/is-deployment.toml" \
    "TOML_BEGIN=$TOML_BEGIN" "TOML_END=$TOML_END" \
    "CDS_BASE=$(toml_str "$CDS_BASE")" "IS_BASE=$(toml_str "$IS_BASE")" \
    "CDS_SYNC_USER=$(toml_str "$CDS_SYNC_USER")" \
    "CDS_SYNC_PASS=$(toml_str "$CDS_SYNC_PASS")"
}

patch_is_config() {
  step "Identity Server configuration"
  local toml="$IS_HOME/repository/conf/deployment.toml"
  [ -f "$toml" ] || die "deployment.toml not found at $toml"

  # Markers are matched on their prefix, so the rest of the text can change
  # without stranding a block written by an earlier version.
  if grep -qF "$TOML_BEGIN_MARK" "$toml"; then
    sed "/^$TOML_BEGIN_MARK/,/^$TOML_END_MARK/d" "$toml" > "$toml.tmp" \
      && mv "$toml.tmp" "$toml" \
      || die "failed to remove the previously generated section from $toml"
    info "replaced the previously generated section"
  fi
  printf '\n' >> "$toml"
  cds_toml_block >> "$toml"
  ok "wrote the CDS section into repository/conf/deployment.toml"

  # [server] already exists in the shipped file, so the offset goes inside it
  # rather than into a second [server] table. Another table may carry an offset
  # of its own, so only lines inside [server] are touched.
  grep -q '^\[server\]' "$toml" || die "no [server] table in $toml"
  awk -v off="$IS_OFFSET" '
    /^\[/                                { in_server = ($0 == "[server]") }
    in_server && /^[ \t]*offset[ \t]*=/  { next }
    { print }
    in_server && $0 == "[server]" && off + 0 > 0 { print "offset = " off }
  ' "$toml" > "$toml.tmp" && mv "$toml.tmp" "$toml" \
    || die "failed to set [server] offset in $toml"
  [ "$IS_OFFSET" = "0" ] || ok "set [server] offset = $IS_OFFSET (HTTPS $IS_PORT, HTTP $IS_HTTP_PORT)"

  check_console_support
}

# The Console renders the Customer Data section from the toml keys written
# above. Its configuration template and its web app ship as one identity-apps
# artifact, so a pack without the key has no CDS support to configure.
check_console_support() {
  local j2
  j2="$IS_HOME/repository/resources/conf/templates/repository/deployment/server/webapps/console/deployment.config.json.j2"
  if [ ! -f "$j2" ]; then
    warn "Console config template not found - skipping the compatibility check"
    return 0
  fi
  if ! grep -q 'cds_host' "$j2"; then
    warn "this pack's Console predates CDS support - the Customer Data section will not appear (build a current pack: drop --is-zip, or use --is-rebuild)"
    return 0
  fi
  ok "bundled Console supports [console.extensions] cds_host"
  grep -q 'customer_data_profiles' "$j2" \
    || warn "bundled Console has cds_host but no customerData feature blocks - the Customer Data section may not appear"
}

# --------------------------------------------------------------------------- #
# 8. Start / stop the Identity Server
# --------------------------------------------------------------------------- #
start_is() {
  step "Starting the Identity Server"
  resolve_java

  if curl -sk -o /dev/null --max-time 5 "$IS_BASE/api/health-check/v1.0/health" 2>/dev/null; then
    if [ "$(curl -sk -o /dev/null -w '%{http_code}' --max-time 5 "$IS_BASE/api/health-check/v1.0/health")" = "200" ]; then
      ok "already running at $IS_BASE"
      return 0
    fi
  fi

  rm -f "$IS_HOME/repository/logs/wso2carbon.log"
  ( cd "$IS_HOME" && ./bin/wso2server.sh start >"$LOG_DIR/is-start.out" 2>&1 ) \
    || die "wso2server.sh start failed - see $LOG_DIR/is-start.out"
  ln -sf "$IS_HOME/repository/logs/wso2carbon.log" "$LOG_DIR/is.log" 2>/dev/null || :

  wait_http "$IS_BASE/api/health-check/v1.0/health" 420 "IS" \
    || die "IS did not start. Tail: $(tail -5 "$IS_HOME/repository/logs/wso2carbon.log" 2>/dev/null)"
  read_pid "$IS_HOME/wso2carbon.pid" > "$RUN_DIR/is.pid" 2>/dev/null || :
  verify_extensions_loaded
}

verify_extensions_loaded() {
  local log="$IS_HOME/repository/logs/wso2carbon.log" missing="" b
  [ -f "$log" ] || return 0
  for b in $EXT_BUNDLES; do
    grep -q "$b" "$log" || missing="$missing $b"
  done
  if [ -n "$missing" ]; then
    info "no startup log entries for:$missing (the bundles log lazily)"
  fi
  if grep -qi "ERROR.*customer.data.service\|Unresolved constraint.*customer.data.service" "$log"; then
    warn "the log mentions errors for the CDS bundles - check $LOG_DIR/is.log"
  else
    ok "no CDS bundle resolution errors in the startup log"
  fi
}

stop_is() {
  local home
  home="$(state_get IS_HOME)"
  [ -n "$home" ] || home="$(resolve_is_home 2>/dev/null || :)"
  local pid=""
  [ -n "$home" ] && pid="$(read_pid "$home/wso2carbon.pid" 2>/dev/null || :)"
  [ -n "$pid" ] || pid="$(read_pid "$RUN_DIR/is.pid" 2>/dev/null || :)"
  if [ -n "$pid" ] && kill -0 "$pid" 2>/dev/null; then
    info "stopping the Identity Server (pid $pid)"
    if stop_pid "$pid" "IS" 120; then
      ok "IS stopped"
      [ -n "$home" ] && rm -f "$home/wso2carbon.pid"
    else
      warn "IS (pid $pid) is still running"
    fi
  else
    info "IS is not running"
  fi
  rm -f "$RUN_DIR/is.pid"
}

# --------------------------------------------------------------------------- #
# 9. Provisioning the Identity Server over its management APIs
# --------------------------------------------------------------------------- #
CDS_SCOPES="internal_cds_profile_view internal_cds_profile_create internal_cds_profile_update internal_cds_profile_delete
internal_cds_profile_schema_view internal_cds_profile_schema_create internal_cds_profile_schema_update internal_cds_profile_schema_delete
internal_cds_unification_rule_view internal_cds_unification_rule_create internal_cds_unification_rule_update internal_cds_unification_rule_delete
internal_cds_admin_config_view internal_cds_admin_config_update
internal_cds_consent_category_view internal_cds_consent_category_create internal_cds_consent_category_update internal_cds_consent_category_delete"

CDS_API_IDENTIFIERS="/cds/api/v1/profiles
/cds/api/v1/profile-schema
/cds/api/v1/unification-rules
/cds/api/v1/config
/cds/api/v1/consent-categories"

api_resource_id() {
  isapi "$MGMT_API/api-resources?filter=identifier+eq+$(urlencode "$1")" \
    | jq -r '.apiResources[0].id // empty'
}

api_resource_scopes() {
  isapi "$MGMT_API/api-resources/$1/scopes" | jq -r '.[]?.name' 2>/dev/null
}

app_id_by_name() {
  isapi "$MGMT_API/applications?filter=name+eq+$(urlencode "$1")" \
    | jq -r '.applications[0].id // empty'
}

oidc_config() { isapi "$MGMT_API/applications/$1/inbound-protocols/oidc"; }

# create_m2m_app NAME IS_MANAGEMENT_APP(true|false)
create_m2m_app() {
  local name="$1" mgmt="$2" body resp code
  body="$(jq -n --arg n "$name" --argjson mgmt "$mgmt" --argjson exp "$TOKEN_EXPIRY" '{
    name: $n,
    description: "Created by scripts/local-setup/script.sh for the Customer Data Service",
    isManagementApp: $mgmt,
    inboundProtocolConfiguration: { oidc: {
      grantTypes: ["client_credentials"],
      accessToken: { type: "JWT", userAccessTokenExpiryInSeconds: $exp, applicationAccessTokenExpiryInSeconds: $exp }
    }}
  }')"
  resp="$(isapi -X POST "$MGMT_API/applications" -H 'Content-Type: application/json' \
    -d "$body" -w '\n%{http_code}')"
  code="$(echo "$resp" | tail -1)"
  case "$code" in
    200|201) ;;
    *) warn "creating '$name' returned HTTP $code: $(echo "$resp" | sed '$d' | head -3)" ;;
  esac
  app_id_by_name "$name"
}

# authorize_api APP_ID API_IDENTIFIER SCOPES...
authorize_api() {
  local app_id="$1" identifier="$2"; shift 2
  local res_id scopes_json present
  res_id="$(api_resource_id "$identifier")"
  [ -n "$res_id" ] || return 1
  scopes_json="$(printf '%s\n' "$@" | jq -R . | jq -s .)"
  present="$(isapi "$MGMT_API/applications/$app_id/authorized-apis" | jq -r --arg id "$res_id" '.[]?|select(.id==$id)|.id')"
  if [ -z "$present" ]; then
    isapi -X POST "$MGMT_API/applications/$app_id/authorized-apis" -H 'Content-Type: application/json' \
      -d "$(jq -n --arg id "$res_id" --argjson s "$scopes_json" '{id:$id, policyIdentifier:"RBAC", scopes:$s}')" -o /dev/null
  else
    isapi -X PATCH "$MGMT_API/applications/$app_id/authorized-apis/$res_id" -H 'Content-Type: application/json' \
      -d "$(jq -n --argjson s "$scopes_json" '{addedScopes:$s, removedScopes:[]}')" -o /dev/null
  fi
}

assert_api_resources() {
  local ident missing=""
  for ident in $CDS_API_IDENTIFIERS; do
    [ -n "$(api_resource_id "$ident")" ] || missing="$missing $ident"
  done
  [ -z "$missing" ] || die "IS did not register the CDS API resource(s):$missing
The [[api_resources]] entries in $IS_HOME/repository/conf/deployment.toml were not picked up.
Re-run with --clean to start the pack from a fresh database."
  ok "all 5 CDS API resources registered"
}

provision_cds_profile_claim() {
  local uri="http://wso2.org/claims/cdsProfile"
  local existing
  existing="$(isapi "$MGMT_API/claim-dialects/local/claims" \
    | jq -r --arg u "$uri" '.[]?|select(.claimURI==$u)|.id')"
  if [ -n "$existing" ]; then
    ok "local claim $uri already exists"
  else
    local body resp
    body="$(jq -n --arg u "$uri" '{
      claimURI: $u,
      description: "Customer Data Service profile identifier",
      displayName: "CDS Profile",
      displayOrder: 0,
      readOnly: false,
      required: false,
      supportedByDefault: false,
      attributeMapping: [{ mappedAttribute: "cdsProfile", userstore: "PRIMARY" }],
      properties: []
    }')"
    resp="$(isapi -X POST "$MGMT_API/claim-dialects/local/claims" -H 'Content-Type: application/json' \
      -d "$body" -w '\n%{http_code}')"
    case "$(echo "$resp" | tail -1)" in
      201|200) ok "created local claim $uri (attribute cdsProfile)" ;;
      *) die "failed to create the local claim $uri: $(echo "$resp" | sed '$d')" ;;
    esac
  fi

  # SCIM2 mapping, so the attribute is reachable through /scim2/Users.
  local dialect_id
  dialect_id="$(isapi "$MGMT_API/claim-dialects" \
    | jq -r '(if type=="array" then . else .dialects end)[]?|select(.dialectURI=="urn:scim:wso2:schema")|.id')"
  if [ -z "$dialect_id" ]; then
    warn "dialect urn:scim:wso2:schema not found - skipping the SCIM2 claim mapping"
    return 0
  fi
  local scim_uri="urn:scim:wso2:schema:cdsProfile" have
  have="$(isapi "$MGMT_API/claim-dialects/$dialect_id/claims" \
    | jq -r --arg u "$scim_uri" '.[]?|select(.claimURI==$u)|.id')"
  if [ -n "$have" ]; then
    ok "SCIM2 claim $scim_uri already mapped"
  else
    isapi -X POST "$MGMT_API/claim-dialects/$dialect_id/claims" -H 'Content-Type: application/json' \
      -d "$(jq -n --arg u "$scim_uri" --arg l "$uri" '{claimURI:$u, mappedLocalClaimURI:$l}')" -o /dev/null
    ok "mapped $scim_uri to $uri"
  fi
}

provision_is() {
  step "Provisioning the Identity Server"
  assert_api_resources
  provision_cds_profile_claim

  # --- The application CDS uses to call IS management APIs ---
  local sys_id
  sys_id="$(app_id_by_name "$SYS_APP_NAME")"
  if [ -z "$sys_id" ]; then
    sys_id="$(create_m2m_app "$SYS_APP_NAME" true)"
    [ -n "$sys_id" ] || die "failed to create '$SYS_APP_NAME'"
    info "created '$SYS_APP_NAME'"
  else
    info "reusing '$SYS_APP_NAME'"
  fi
  # Exactly the scopes IdentityClient.FetchToken asks for.
  authorize_api "$sys_id" "/api/server/v1/applications" internal_application_mgt_view \
    || die "management API resource /api/server/v1/applications not found"
  authorize_api "$sys_id" "/api/server/v1/claim-dialects" internal_claim_meta_view \
    || die "management API resource /api/server/v1/claim-dialects not found"
  authorize_api "$sys_id" "/scim2/Users" internal_user_mgt_list internal_user_mgt_view \
    || die "management API resource /scim2/Users not found"
  # B2B: CDS reads the org tree of the root org.
  authorize_api "$sys_id" "/api/server/v1/organizations" internal_organization_view \
    || die "management API resource /api/server/v1/organizations not found"
  ok "'$SYS_APP_NAME' authorized for applications, claim-dialects, scim2/Users and organizations"

  SYS_CLIENT_ID="$(oidc_config "$sys_id" | jq -r '.clientId // empty')"
  SYS_CLIENT_SECRET="$(oidc_config "$sys_id" | jq -r '.clientSecret // empty')"
  [ -n "$SYS_CLIENT_ID" ] || die "could not read the client ID of '$SYS_APP_NAME'"
  state_set SYS_CLIENT_ID "$SYS_CLIENT_ID"
  state_set SYS_CLIENT_SECRET "$SYS_CLIENT_SECRET"

  # --- A client application for calling the CDS APIs ---
  local cli_id
  cli_id="$(app_id_by_name "$CLIENT_APP_NAME")"
  if [ -z "$cli_id" ]; then
    cli_id="$(create_m2m_app "$CLIENT_APP_NAME" true)"
    [ -n "$cli_id" ] || die "failed to create '$CLIENT_APP_NAME'"
    info "created '$CLIENT_APP_NAME'"
  else
    info "reusing '$CLIENT_APP_NAME'"
  fi
  set_token_audience "$cli_id"
  local ident scopes
  for ident in $CDS_API_IDENTIFIERS; do
    scopes="$(api_resource_scopes "$(api_resource_id "$ident")" | tr '\n' ' ')"
    [ -n "$scopes" ] || die "no scopes found on API resource $ident"
    authorize_api "$cli_id" "$ident" $scopes || die "failed to authorize $ident"
  done
  ok "'$CLIENT_APP_NAME' authorized for all 5 CDS API resources, audience '$AUDIENCE'"

  CLIENT_ID="$(oidc_config "$cli_id" | jq -r '.clientId // empty')"
  CLIENT_SECRET="$(oidc_config "$cli_id" | jq -r '.clientSecret // empty')"
  state_set CLIENT_ID "$CLIENT_ID"
  state_set CLIENT_SECRET "$CLIENT_SECRET"

  provision_console_app
}

# Adds $AUDIENCE to an application's token audience, for the audience check in
# internal/system/authn.
set_token_audience() {
  local app_id="$1" current patched
  current="$(oidc_config "$app_id")"
  echo "$current" | jq -e --arg a "$AUDIENCE" '.idToken.audience // [] | index($a)' >/dev/null 2>&1 && return 0
  patched="$(echo "$current" | jq --arg a "$AUDIENCE" '.idToken.audience = ((.idToken.audience // []) + [$a] | unique)')"
  isapi -X PUT "$MGMT_API/applications/$app_id/inbound-protocols/oidc" \
    -H 'Content-Type: application/json' -d "$patched" -o /dev/null
}

# The Console calls CDS with its own token, so it needs the CDS scopes and, for
# JWTs, the CDS audience.
provision_console_app() {
  local console_id token_type
  console_id="$(isapi "$MGMT_API/applications?filter=name+eq+Console" | jq -r '.applications[0].id // empty')"
  if [ -z "$console_id" ]; then
    warn "could not find the Console application - the Console UI may not reach CDS"
    return 0
  fi
  token_type="$(oidc_config "$console_id" | jq -r '.accessToken.type // empty')"
  info "Console access token type: ${token_type:-unknown}"
  if [ "$token_type" = "JWT" ]; then
    set_token_audience "$console_id" && ok "added '$AUDIENCE' to the Console token audience" \
      || warn "could not set the Console token audience"
  else
    ok "Console uses opaque tokens - CDS accepts those for the Console client"
  fi

  local ident scopes failed=""
  for ident in $CDS_API_IDENTIFIERS; do
    scopes="$(api_resource_scopes "$(api_resource_id "$ident")" | tr '\n' ' ')"
    authorize_api "$console_id" "$ident" $scopes >/dev/null 2>&1 || failed="$failed $ident"
  done
  if [ -n "$failed" ]; then
    warn "could not authorize the Console application for:$failed"
  else
    ok "Console application authorized for the CDS API resources"
  fi
  grant_admin_role_scopes
}

# The tenant administrator has to hold the CDS scopes.
grant_admin_role_scopes() {
  local role_id perms add s
  role_id="$(isapi "$IS_BASE/scim2/v2/Roles?filter=displayName+eq+admin" \
    | jq -r '.Resources[0].id // empty' 2>/dev/null)"
  if [ -z "$role_id" ]; then
    warn "could not resolve the 'admin' role - grant the CDS scopes from the Console if the pages are empty"
    return 0
  fi
  perms="$(isapi "$IS_BASE/scim2/v2/Roles/$role_id" | jq -r '.permissions[]?.value' 2>/dev/null)"
  add=""
  for s in $CDS_SCOPES console:customerData console:customerData_view console:customerData_edit; do
    echo "$perms" | grep -qx "$s" || add="$add $s"
  done
  if [ -z "$add" ]; then
    ok "the 'admin' role already holds every CDS scope"
    return 0
  fi
  local value
  value="$(printf '%s\n' $add | jq -R '{value:.}' | jq -s .)"
  if isapi -X PATCH "$IS_BASE/scim2/v2/Roles/$role_id" -H 'Content-Type: application/json' \
      -d "$(jq -n --argjson v "$value" '{schemas:["urn:ietf:params:scim:api:messages:2.0:PatchOp"],
             Operations:[{op:"add", path:"permissions", value:$v}]}')" \
      -o /dev/null -w '%{http_code}' | grep -qE '^(200|204)$'; then
    ok "granted $(printf '%s\n' $add | wc -l | tr -d ' ') scope(s) to the 'admin' role"
  else
    warn "could not patch the 'admin' role permissions - add the Customer Data scopes from the Console if needed"
  fi
}

# --------------------------------------------------------------------------- #
# 10. CDS configuration
# --------------------------------------------------------------------------- #
render_cds_config() {
  step "CDS configuration"
  local base="$REPO_DIR/config/repository/conf/deployment.yaml"
  local target="$CDS_HOME/repository/conf/deployment.yaml"
  [ -f "$base" ] || die "base config not found at $base"
  mkdir -p "$(dirname "$target")"

  # The template is a full configuration rather than an overlay, so a section
  # added to the shipped file upstream would be missed. Say so.
  local missing
  missing="$(comm -23 \
    <(grep -oE '^[a-z_]+:' "$base" | sort -u) \
    <(cat "$TPL_DIR/cds-deployment.yaml" "$TPL_DIR/datasource-$DB.yaml" \
        | grep -oE '^[a-z_]+:' | sort -u) | tr -d ':')"
  [ -z "$missing" ] || warn "templates/cds-deployment.yaml has no section for: $missing"

  # required_scopes is taken from the shipped configuration so the
  # operation-to-scope mapping cannot drift.
  {
    cat "$TPL_DIR/cds-deployment.yaml"
    yaml_block "$base" required_scopes
    cat "$TPL_DIR/datasource-$DB.yaml"
  } > "$target" || die "failed to write $target"
  grep -q 'required_scopes:' "$target" || die "no required_scopes block found in $base"
  ok "wrote $target"
}

# --------------------------------------------------------------------------- #
# 11. Start / stop CDS
# --------------------------------------------------------------------------- #
start_cds() {
  step "Starting CDS"
  if is_running "$RUN_DIR/cds.pid"; then
    info "stopping the running instance first"
    stop_cds
  fi
  mkdir -p "$CDS_HOME/repository/database"
  # Run from the work directory: CDS globs <cwd>/config/*.env for environment
  # files, and nothing in the repository should be picked up.
  cd "$WORK_DIR"
  # CDS expands ${...} in its configuration from the environment at load time,
  # which is what keeps the secrets out of deployment.yaml.
  CDS_HOME="$CDS_HOME" \
  CDS_LOG_LEVEL="$CDS_LOG_LEVEL" \
  CDS_LISTEN_HOST="$CDS_LISTEN_HOST" CDS_PORT="$CDS_PORT" CDS_BASE="$CDS_BASE" \
  CDS_CERT_DIR="$CERT_DIR" \
  IS_HOST="$IS_HOST" IS_PORT="$IS_PORT" IS_BASE="$IS_BASE" \
  CDS_SYNC_USER="$CDS_SYNC_USER" \
  CDS_SYS_CLIENT_ID="${SYS_CLIENT_ID:-}" \
  AUTH_SERVER_CLIENT_SECRET="${SYS_CLIENT_SECRET:-}" \
  INTROSPECTION_CLIENT_SECRET="${SYS_CLIENT_SECRET:-}" \
  AUTH_SERVER_ADMIN_PASSWORD="$CDS_SYNC_PASS" \
  PG_HOST="$PG_HOST" PG_PORT="$PG_PORT" PG_USER="$PG_USER" PG_DB="$PG_DB" \
  DB_PASSWORD="$PG_PASS" \
    nohup "$BIN_DIR/cds" >>"$LOG_DIR/cds.log" 2>&1 </dev/null &
  echo $! > "$RUN_DIR/cds.pid"
  cd "$REPO_DIR"
  sleep 1
  is_running "$RUN_DIR/cds.pid" || die "CDS exited immediately. Tail:
$(tail -20 "$LOG_DIR/cds.log")"
  wait_http "$CDS_URL_LOCAL/cds/api/v1/health" 60 "CDS" \
    || die "CDS did not become healthy. Tail:
$(tail -20 "$LOG_DIR/cds.log")"
  wait_http "$CDS_URL_LOCAL/cds/api/v1/ready" 60 "CDS database" \
    || die "CDS is up but its database is not ready. Tail:
$(tail -20 "$LOG_DIR/cds.log")"
  ok "CDS listening on $CDS_URL_LOCAL (pid $(read_pid "$RUN_DIR/cds.pid"))"
}

stop_cds() {
  if is_running "$RUN_DIR/cds.pid"; then
    local pid
    pid="$(read_pid "$RUN_DIR/cds.pid")"
    if stop_pid "$pid" "CDS" 20; then
      ok "CDS stopped"
    else
      warn "CDS (pid $pid) is still running"
    fi
  else
    info "CDS is not running"
  fi
  rm -f "$RUN_DIR/cds.pid"
}

# --------------------------------------------------------------------------- #
# 12. Enable CDS for the organization
# --------------------------------------------------------------------------- #
# Every profile, schema and rule endpoint returns 400 while cds_enabled is false
# for the organization. Setting it also runs the initial profile-schema sync
# from IS and seeds the default consent category.
enable_cds_for_org() {
  step "Enabling CDS for '$TENANT'"
  local token state body code
  token="$(cds_token)"
  [ -n "$token" ] || die "could not get a token to call the CDS config API"

  state="$(curl -sk --max-time 30 -H "Authorization: Bearer $token" \
    "$CDS_URL_LOCAL/t/$TENANT/cds/api/v1/config" | jq -r '.cds_enabled // false' 2>/dev/null)"
  if [ "$state" = "true" ]; then
    ok "already enabled for '$TENANT'"
    return 0
  fi

  # Admin surfaces see unfiltered profiles; regular applications get consent-filtered data.
  body="$(jq -n --arg console "CONSOLE" --arg cli "${CLIENT_ID:-}" \
    '{cds_enabled: true, system_applications: ([$console, $cli] | map(select(. != "")))}')"
  code="$(curl -sk --max-time 120 -o "$LOG_DIR/enable-cds.json" -w '%{http_code}' \
    -X PATCH -H "Authorization: Bearer $token" -H 'Content-Type: application/json' \
    -d "$body" "$CDS_URL_LOCAL/t/$TENANT/cds/api/v1/config")"
  case "$code" in
    200|204)
      ok "cds_enabled = true, initial profile-schema sync done, default consent category seeded"
      ;;
    *)
      die "could not enable CDS for '$TENANT' (HTTP $code): $(cat "$LOG_DIR/enable-cds.json" 2>/dev/null)
This runs the initial schema sync, so it fails when CDS cannot reach the IS claim APIs.
Check $LOG_DIR/cds.log."
      ;;
  esac
}

# --------------------------------------------------------------------------- #
# 13. Smoke tests
# --------------------------------------------------------------------------- #
TESTS_RUN=0
TESTS_FAILED=0

t_pass() { TESTS_RUN=$((TESTS_RUN + 1)); ok "$1"; }
t_fail() { TESTS_RUN=$((TESTS_RUN + 1)); TESTS_FAILED=$((TESTS_FAILED + 1)); printf '    %sx%s %s\n' "${C_RED}" "${C_RESET}" "$1" >&2; }

cds_token() {
  local scopes
  scopes="$(echo $CDS_SCOPES | tr '\n' ' ')"
  curl -sk --max-time 30 -u "${CLIENT_ID}:${CLIENT_SECRET}" \
    "$IS_BASE/t/$TENANT/oauth2/token" \
    -d grant_type=client_credentials --data-urlencode "scope=$scopes" \
    | jq -r '.access_token // empty'
}

jwt_claim() {
  local payload
  payload="$(printf '%s' "$1" | cut -d. -f2 | tr '_-' '/+')"
  while [ $(( ${#payload} % 4 )) -ne 0 ]; do payload="$payload="; done
  printf '%s' "$payload" | base64 -d 2>/dev/null \
    | jq -r --arg c "$2" '.[$c] // "" | if type == "array" then join(" ") else tostring end' 2>/dev/null \
    || :   # a malformed token is a failed test, not a failed run
}

run_smoke_tests() {
  step "Smoke tests"

  # 1. CDS is up and its database answers.
  if [ "$(curl -sk -o /dev/null -w '%{http_code}' "$CDS_URL_LOCAL/cds/api/v1/ready")" = "200" ]; then
    t_pass "CDS readiness endpoint returns 200 ($DB)"
  else
    t_fail "CDS readiness endpoint did not return 200"
  fi

  # 2. A token for the CDS client application, carrying the CDS audience.
  local token aud org
  token="$(cds_token)"
  if [ -z "$token" ]; then
    t_fail "could not obtain a client_credentials token for '$CLIENT_APP_NAME'"
    return 0
  fi
  aud="$(jwt_claim "$token" aud)"
  org="$(jwt_claim "$token" org_handle)"
  if echo " $aud " | grep -q " $AUDIENCE "; then
    t_pass "issued token carries aud=$AUDIENCE"
  else
    t_fail "issued token audience is '$aud', expected to contain '$AUDIENCE'"
  fi
  if [ "$org" = "$TENANT" ]; then
    t_pass "issued token carries org_handle=$TENANT"
  else
    t_fail "issued token org_handle is '$org', expected '$TENANT' (CDS rejects JWTs without it)"
  fi

  # 3. CDS is switched on for the organization.
  local code
  if [ "$(curl -sk --max-time 30 -H "Authorization: Bearer $token" \
        "$CDS_URL_LOCAL/t/$TENANT/cds/api/v1/config" | jq -r '.cds_enabled // false')" = "true" ]; then
    t_pass "CDS is enabled for '$TENANT'"
  else
    t_fail "CDS is not enabled for '$TENANT'"
  fi

  # 4. An authorized CDS call: covers introspection and scope mapping.
  code="$(curl -sk -o /dev/null -w '%{http_code}' -H "Authorization: Bearer $token" \
    "$CDS_URL_LOCAL/t/$TENANT/cds/api/v1/profiles")"
  if [ "$code" = "200" ]; then
    t_pass "GET /t/$TENANT/cds/api/v1/profiles returns 200"
  else
    t_fail "GET /t/$TENANT/cds/api/v1/profiles returned $code (expected 200)"
  fi

  # 5. The CDS -> IS direction: this handler reads the IS claim dialects.
  code="$(curl -sk -o /dev/null -w '%{http_code}' -H "Authorization: Bearer $token" \
    "$CDS_URL_LOCAL/t/$TENANT/cds/api/v1/profile-schema")"
  if [ "$code" = "200" ]; then
    t_pass "GET /t/$TENANT/cds/api/v1/profile-schema returns 200 (CDS reached the IS claim APIs)"
  else
    t_fail "GET /t/$TENANT/cds/api/v1/profile-schema returned $code - CDS could not call IS (check the system app and trust store)"
  fi

  test_is_to_cds_sync "$token"
}

# Covers the IS -> CDS direction end to end: the dropin bundles, the event
# handler subscriptions and the shared Basic credentials.
test_is_to_cds_sync() {
  local token="$1"
  local uname="cds-smoke-$$" upass
  # Mixed case, digits and a symbol, for the default password policy.
  upass="Smoke@$(openssl rand -hex 8)"
  local user_id
  user_id="$(isapi -X POST "$IS_BASE/scim2/Users" -H 'Content-Type: application/scim+json' \
    -d "$(jq -n --arg u "$uname" --arg p "$upass" '{
      schemas:["urn:ietf:params:scim:schemas:core:2.0:User"],
      userName:("PRIMARY/" + $u),
      password:$p,
      name:{givenName:"CDS", familyName:"Smoke"},
      emails:[{value:($u + "@example.com"), primary:true}]
    }')" | jq -r '.id // empty')"
  if [ -z "$user_id" ]; then
    t_fail "could not create a test user in IS - skipping the sync test"
    return 0
  fi

  local waited=0 found="0"
  while [ "$waited" -lt 30 ]; do
    if curl -sk --max-time 20 -H "Authorization: Bearer $token" \
         "$CDS_URL_LOCAL/t/$TENANT/cds/api/v1/profiles" 2>/dev/null | grep -q "$user_id"; then
      found="1"; break
    fi
    sleep 2; waited=$((waited + 2))
  done

  if [ "$found" = "1" ]; then
    t_pass "IS -> CDS sync: a profile appeared for the new IS user"
  else
    t_fail "IS -> CDS sync: no CDS profile appeared for user $uname within ${waited}s (check $LOG_DIR/cds.log and $LOG_DIR/is.log)"
  fi

  if [ "$KEEP_TEST_USER" = "1" ]; then
    info "keeping the test user $uname ($user_id)"
  else
    isapi -X DELETE "$IS_BASE/scim2/Users/$user_id" -o /dev/null
  fi
}

# --------------------------------------------------------------------------- #
# 14. Summary
# --------------------------------------------------------------------------- #
print_summary() {
  local db_desc wd_opt=""
  [ "$WORK_DIR" = "$REPO_DIR/.local-dev" ] || wd_opt=" --work-dir '$WORK_DIR'"
  if [ "$DB" = "sqlite" ]; then
    db_desc="SQLite at $CDS_HOME/repository/database/cds.db"
  else
    db_desc="PostgreSQL $PG_USER@$PG_HOST:$PG_PORT/$PG_DB"
  fi
  cat <<SUMMARY

  Console          $IS_BASE/console
  My Account       $IS_BASE/myaccount
  CDS              $CDS_BASE   (listening on $CDS_URL_LOCAL)
  CDS database     $db_desc

  Logs             $LOG_DIR/cds.log
                   $LOG_DIR/is.log
  Work dir         $WORK_DIR
  Stop             scripts/local-setup/script.sh down$wd_opt
  Start again      scripts/local-setup/script.sh start$wd_opt

SUMMARY
}

# --------------------------------------------------------------------------- #
# 15. Commands
# --------------------------------------------------------------------------- #
cmd_up() {
  preflight
  resolve_secrets

  if [ "$SKIP_CDS" != "1" ]; then
    build_cds
    generate_cds_cert
    setup_database
  else
    generate_cds_cert
  fi

  if [ "$SKIP_IS" != "1" ]; then
    fetch_is_pack
    deploy_extensions
    exchange_certificates
    patch_is_config
    start_is
    provision_is
  else
    IS_HOME="$(state_get IS_HOME)"
    SYS_CLIENT_ID="$(state_get SYS_CLIENT_ID)"
    SYS_CLIENT_SECRET="$(state_get SYS_CLIENT_SECRET)"
    CLIENT_ID="$(state_get CLIENT_ID)"
    CLIENT_SECRET="$(state_get CLIENT_SECRET)"
    [ -n "$SYS_CLIENT_ID" ] || die "--skip-is needs a previous run: no client IDs in $STATE_FILE"
    info "reusing the applications recorded in $STATE_FILE"
  fi

  if [ "$SKIP_CDS" != "1" ]; then
    render_cds_config
    start_cds
  fi

  if [ "$SKIP_CDS" != "1" ] && [ "$SKIP_IS" != "1" ]; then
    enable_cds_for_org
  fi

  if [ "$SKIP_TESTS" != "1" ] && [ "$SKIP_CDS" != "1" ] && [ "$SKIP_IS" != "1" ]; then
    run_smoke_tests
  fi

  # Recorded last, so `start` only inherits settings from a completed run.
  save_run_settings
  print_summary

  if [ "$TESTS_FAILED" -gt 0 ]; then
    die "$TESTS_FAILED of $TESTS_RUN smoke test(s) failed - see the log files above"
  fi
  [ "$TESTS_RUN" -eq 0 ] || ok "all $TESTS_RUN smoke tests passed"
}

# Starts an already-provisioned work directory: no build, clone, maven or REST
# provisioning. The applications and configuration are the ones `up` left.
cmd_start() {
  step "Quick start"
  require_bins curl jq
  [ "$SKIP_IS" = "1" ] || require_bins java
  if [ "$SKIP_CDS" != "1" ] && [ "$DB" = "postgres" ] && [ "$PG_EXTERNAL" != "1" ]; then
    require_bins docker
  fi

  IS_HOME="$(state_get IS_HOME)"
  SYS_CLIENT_ID="$(state_get SYS_CLIENT_ID)"
  SYS_CLIENT_SECRET="$(state_get SYS_CLIENT_SECRET)"
  CLIENT_ID="$(state_get CLIENT_ID)"
  CLIENT_SECRET="$(state_get CLIENT_SECRET)"

  local hint="run 'scripts/local-setup/script.sh up --work-dir \"$WORK_DIR\"' first"
  [ -f "$STATE_FILE" ] || die "$WORK_DIR has not been set up yet - $hint"
  [ -n "$(state_get DB)" ] \
    || die "$WORK_DIR records no settings - $hint"
  if [ "$SKIP_IS" != "1" ]; then
    [ -n "$IS_HOME" ] && [ -x "$IS_HOME/bin/wso2server.sh" ] \
      || die "no Identity Server pack in $WORK_DIR - $hint"
    [ -n "$CLIENT_ID" ] || die "no OAuth applications recorded in $STATE_FILE - $hint"
  fi
  if [ "$SKIP_CDS" != "1" ]; then
    [ -x "$BIN_DIR/cds" ] || die "CDS has not been built in $WORK_DIR - $hint"
    [ -f "$CDS_HOME/repository/conf/deployment.yaml" ] \
      || die "no CDS configuration in $CDS_HOME - $hint"
  fi
  ok "reusing the setup in $WORK_DIR (CDS datasource: $DB)"

  if [ "$SKIP_CDS" != "1" ] && ! is_running "$RUN_DIR/cds.pid"; then
    ensure_port_free "$CDS_PORT" "CDS"
  fi
  if [ "$SKIP_IS" != "1" ] && ! is_running "$RUN_DIR/is.pid"; then
    ensure_port_free "$IS_PORT" "IS HTTPS"
    ensure_port_free "$IS_HTTP_PORT" "IS HTTP"
  fi

  # Idempotent: restarts the container and applies the schema only if the
  # database is empty, which it is when the container was removed.
  [ "$SKIP_CDS" = "1" ] || setup_database

  [ "$SKIP_IS" = "1" ] || start_is
  [ "$SKIP_CDS" = "1" ] || start_cds

  if [ "$SKIP_CDS" != "1" ] && [ "$SKIP_IS" != "1" ]; then
    enable_cds_for_org
  fi

  # Opt-in: the tests create and delete an IS user.
  if [ "$WITH_TESTS" = "1" ] && [ "$SKIP_CDS" != "1" ] && [ "$SKIP_IS" != "1" ]; then
    run_smoke_tests
  fi

  print_summary

  if [ "$TESTS_FAILED" -gt 0 ]; then
    die "$TESTS_FAILED of $TESTS_RUN smoke test(s) failed - see the log files above"
  fi
  [ "$TESTS_RUN" -eq 0 ] || ok "all $TESTS_RUN smoke tests passed"
}

cmd_restart() {
  cmd_down
  cmd_start
}

cmd_down() {
  step "Stopping"
  stop_cds
  [ "$SKIP_IS" = "1" ] || stop_is
  # The state file records whether this work directory started the container.
  if [ "$(state_get CDS_PG_CONTAINER_OWNED)" = "1" ] && command -v docker >/dev/null 2>&1; then
    local container
    container="$(state_get CDS_PG_CONTAINER)"
    [ -n "$container" ] || container="$PG_CONTAINER"
    if docker ps -a --format '{{.Names}}' | grep -qx "$container"; then
      if docker rm -f "$container" >/dev/null 2>&1; then
        ok "removed container '$container'"
        state_set CDS_PG_CONTAINER_OWNED "0"
      else
        warn "could not remove the container '$container' - remove it by hand"
      fi
    else
      state_set CDS_PG_CONTAINER_OWNED "0"
    fi
  fi
  if [ "$PURGE" = "1" ]; then
    rm -rf "$WORK_DIR"
    ok "removed $WORK_DIR"
  fi
}

cmd_status() {
  step "Status"
  local code
  if is_running "$RUN_DIR/cds.pid"; then
    code="$(curl -sk -o /dev/null -w '%{http_code}' --max-time 5 "$CDS_URL_LOCAL/cds/api/v1/ready" || echo 000)"
    info "CDS   running (pid $(read_pid "$RUN_DIR/cds.pid")), /ready -> $code"
  else
    info "CDS   not running"
  fi
  local home
  home="$(state_get IS_HOME)"
  if [ -n "$home" ] && is_running "$home/wso2carbon.pid"; then
    code="$(curl -sk -o /dev/null -w '%{http_code}' --max-time 5 "$IS_BASE/api/health-check/v1.0/health" || echo 000)"
    info "IS    running (pid $(read_pid "$home/wso2carbon.pid")), health -> $code"
  else
    info "IS    not running"
  fi
  if [ "$DB" = "postgres" ] && [ "$PG_EXTERNAL" != "1" ]; then
    if docker ps --format '{{.Names}}' | grep -qx "$PG_CONTAINER"; then
      info "PG    container '$PG_CONTAINER' running"
    else
      info "PG    container '$PG_CONTAINER' not running"
    fi
  fi
  info "work  $WORK_DIR"
}

cmd_logs() {
  local files="" f
  case "${LOGS_TARGET:-}" in
    cds) files="$LOG_DIR/cds.log" ;;
    is)  files="$LOG_DIR/is.log" ;;
    *)   files="$LOG_DIR/cds.log $LOG_DIR/is.log" ;;
  esac
  local present=""
  for f in $files; do [ -r "$f" ] && present="$present $f"; done
  [ -n "$present" ] || die "no readable log file yet under $LOG_DIR"
  exec tail -f $present
}

case "$CMD" in
  up) cmd_up ;;
  start) cmd_start ;;
  restart) cmd_restart ;;
  down) cmd_down ;;
  status) cmd_status ;;
  logs) cmd_logs ;;
esac
