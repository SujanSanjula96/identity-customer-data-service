#!/usr/bin/env python3
"""Prepare and control the CDS deployment for the performance test cases T2 to T8.

Runs on the bastion node. The test runner calls it before a test case and
between the test levels. It uses only the Python standard library, psql,
pg_dump, pg_restore, and ssh with the control key of the run.

Subcommands:
    schema                 Make the trait attributes that the tests use.
    rules                  Make the 6 unification rules of rule set R5 (all active).
    ruleset <name>         Activate only the rules of one rule set: NONE, R1 to R5.
                           Changes the database, then restarts CDS (the rules cache).
    decisive <true|false>  Set deterministic_match_decisive in the admin config.
    load-seed <seed.csv>   Make the seed profiles through POST /profiles.
    wait-queue             Wait until the unification queue is empty.
    snapshot <file>        Save the CDS database (pg_dump, custom format).
    restore <file>         Stop CDS, clear the queue, restore the database, start CDS.
    purge-queue            Clear the unification queue (before each scenario).
    pg-stats <file>        Save all rows of pg_stat_statements of the CDS database as CSV.
                           pg-stats-diff.py compares two of these files.
    pg-stats-is <file>     The same for the IS database server (IS_DB_HOST, IS_DB_USER,
                           IS_DB_PASS). The query text starts with [<database name>].
    restart-cds            Restart all CDS nodes and wait until they are ready.
    counts <start epoch> [<end epoch>]
                           The result of the profiles that were made in the window:
                           merged, in a review task, or no action. JSON.
    reindex <rule name> <property> <attribute type> <method>
                           T8: make one active rule and measure the index backfill. JSON.
                           An old rule with the same name is deleted first.
    deactivate <rule name> <property>
                           T8: set the rule inactive and measure the index removal. JSON.

Settings come from the environment, with these defaults for the bastion node:
    CDS_BASE=https://cdslb  IS_BASE=https://wso2is:9443  ORG=carbon.super
    CA_FILE=~/cds-perf/certs/ca.crt  SECRETS=~/cds-perf/secrets.env
    STATE=~/cds-perf/state.env  CDS_NODES (default: the cdsN names in /etc/hosts)
    CDS_TOKEN: a fixed access token. Without it, the tool gets a token from IS
    with CLIENT_ID and CLIENT_SECRET from the state file.
    QUEUE_CSV: the output of queue-monitor.sh. wait-queue uses Jolokia if it is not set.
"""

import base64
import csv
import http.client
import json
import os
import queue
import re
import ssl
import subprocess
import sys
import threading
import time
import urllib.parse
import urllib.request

HOME = os.path.expanduser("~")
CDS_BASE = os.environ.get("CDS_BASE", "https://cdslb")
IS_BASE = os.environ.get("IS_BASE", "https://wso2is:9443")
ORG = os.environ.get("ORG", "carbon.super")
CA_FILE = os.environ.get("CA_FILE", f"{HOME}/cds-perf/certs/ca.crt")
SECRETS = os.environ.get("SECRETS", f"{HOME}/cds-perf/secrets.env")
STATE = os.environ.get("STATE", f"{HOME}/cds-perf/state.env")
API = f"/t/{ORG}/cds/api/v1"

TRAITS = [
    ("email", "string"), ("phone", "string"), ("nic", "string"), ("first_name", "string"),
    ("last_name", "string"), ("address", "string"), ("alt_email", "string"),
    ("alt_name", "string"), ("last_page", "string"), ("page_views", "integer"),
]

# Rule set R5. The other rule sets are subsets (RULE_SETS).
RULES = [
    {"rule_name": "perf-email", "property_name": "traits.email", "priority": 1,
     "attribute_type": "EMAIL", "unification_method": "deterministic"},
    {"rule_name": "perf-phone", "property_name": "traits.phone", "priority": 2,
     "attribute_type": "PHONE", "unification_method": "deterministic"},
    {"rule_name": "perf-nic", "property_name": "traits.nic", "priority": 3,
     "attribute_type": "UNIQUE_ID", "unification_method": "deterministic"},
    {"rule_name": "perf-first-name", "property_name": "traits.first_name", "priority": 4,
     "attribute_type": "NAME", "unification_method": "fuzzy"},
    {"rule_name": "perf-last-name", "property_name": "traits.last_name", "priority": 5,
     "attribute_type": "NAME", "unification_method": "fuzzy"},
    {"rule_name": "perf-address", "property_name": "traits.address", "priority": 6,
     "attribute_type": "LOCATION", "unification_method": "fuzzy"},
]
RULE_SETS = {
    "NONE": [],
    "R1": ["perf-email"],
    "R2": ["perf-first-name"],
    "R3": ["perf-email", "perf-first-name"],
    "R4": ["perf-email", "perf-phone", "perf-first-name", "perf-address"],
    "R5": [r["rule_name"] for r in RULES],
}


def die(msg):
    print(f"ERROR: {msg}", file=sys.stderr)
    sys.exit(1)


def kv_file(path):
    values = {}
    try:
        with open(path) as handle:
            for line in handle:
                line = line.strip()
                if "=" in line and not line.startswith("#"):
                    k, v = line.split("=", 1)
                    values[k.replace("export ", "").strip()] = v.strip().strip('"').strip("'")
    except FileNotFoundError:
        pass
    return values


SECRET = kv_file(SECRETS)
STATE_VALUES = kv_file(STATE)


def env(name, default=""):
    return os.environ.get(name) or SECRET.get(name) or STATE_VALUES.get(name) or default


def ssl_context():
    ctx = ssl.create_default_context(cafile=CA_FILE) if os.path.exists(CA_FILE) \
        else ssl.create_default_context()
    if os.environ.get("INSECURE_TLS") == "1":
        ctx.check_hostname = False
        ctx.verify_mode = ssl.CERT_NONE
    return ctx


SSL = ssl_context()
_token = None


def token():
    global _token
    if _token:
        return _token
    if os.environ.get("CDS_TOKEN"):
        _token = os.environ["CDS_TOKEN"]
        return _token
    cid, secret = env("CLIENT_ID"), env("CLIENT_SECRET")
    if not cid:
        die(f"No CLIENT_ID in {STATE}, and no CDS_TOKEN.")
    scopes = ("internal_cds_profile_create internal_cds_profile_view internal_cds_profile_update "
              "internal_cds_profile_link internal_cds_profile_schema_create "
              "internal_cds_profile_schema_view internal_cds_unification_rule_create "
              "internal_cds_unification_rule_view internal_cds_unification_rule_update "
              "internal_cds_unification_rule_delete internal_cds_admin_config_view "
              "internal_cds_admin_config_update")
    body = urllib.parse.urlencode({"grant_type": "client_credentials", "scope": scopes}).encode()
    req = urllib.request.Request(f"{IS_BASE}/t/{ORG}/oauth2/token", data=body, method="POST")
    req.add_header("Authorization", "Basic " + base64.b64encode(f"{cid}:{secret}".encode()).decode())
    req.add_header("Content-Type", "application/x-www-form-urlencoded")
    with urllib.request.urlopen(req, context=SSL, timeout=30) as resp:
        _token = json.load(resp)["access_token"]
    return _token


def api(method, path, body=None, ok=(200, 201, 204)):
    """One CDS API call with the application token. Returns (status, parsed body)."""
    data = json.dumps(body).encode() if body is not None else None
    req = urllib.request.Request(f"{CDS_BASE}{API}{path}", data=data, method=method)
    req.add_header("Authorization", f"Bearer {token()}")
    req.add_header("Content-Type", "application/json")
    try:
        with urllib.request.urlopen(req, context=SSL, timeout=60) as resp:
            raw = resp.read()
            status = resp.status
    except urllib.error.HTTPError as err:
        raw, status = err.read(), err.code
    try:
        parsed = json.loads(raw) if raw else None
    except ValueError:
        parsed = raw.decode(errors="replace")
    if ok and status not in ok:
        die(f"{method} {path}: HTTP {status}: {parsed}")
    return status, parsed


def psql(sql, csv_out=False, target="cds"):
    # target "is": the "postgres" database of the IS database server.
    if target == "is":
        host, user, db, password = (env("IS_DB_HOST"), env("IS_DB_USER", "postgres"), "postgres",
                                    env("IS_DB_PASS"))
        if not host:
            die("No IS_DB_HOST. The IS query statistics need access to the IS database.")
    else:
        host, user, db, password = (env("CDS_DB_HOST", "localhost"), env("CDS_DB_USER", "postgres"),
                                    env("CDS_DB_NAME", "cds_db"), env("CDS_DB_PASS"))
    cmd = ["psql", "-h", host, "-U", user, "-d", db, "-v", "ON_ERROR_STOP=1", "-qAt"]
    if csv_out:
        cmd += ["--csv"]
    run_env = dict(os.environ, PGPASSWORD=password)
    out = subprocess.run(cmd + ["-c", sql], env=run_env, capture_output=True, text=True)
    if out.returncode != 0:
        die(f"psql: {out.stderr.strip()}")
    return out.stdout.strip()


def org_id():
    value = psql(f"SELECT org_id FROM organizations WHERE org_handle = '{ORG}'")
    if not value:
        die(f"No organization {ORG} in the CDS database. Enable CDS first.")
    return value.splitlines()[0]


def cds_nodes():
    if os.environ.get("CDS_NODES") is not None:
        return os.environ["CDS_NODES"].split()
    names = []
    with open("/etc/hosts") as handle:
        for line in handle:
            m = re.search(r"\s(cds\d)\s+# cds-perf", line)
            if m:
                names.append(m.group(1))
    return sorted(set(names))


def node_ssh(host, command):
    return subprocess.run(["ssh", host, command], capture_output=True, text=True)


# --------------------------------------------------------------------------- #
def cmd_schema():
    body = [{"attribute_name": f"traits.{name}", "value_type": vtype, "merge_strategy": "overwrite",
             "mutability": "readWrite", "multi_valued": False, "display_name": name}
            for name, vtype in TRAITS]
    existing = api("GET", "/profile-schema/traits", ok=None)[1] or []
    names = {a.get("attribute_name") for a in existing} if isinstance(existing, list) else set()
    todo = [a for a in body if a["attribute_name"] not in names]
    if todo:
        api("POST", "/profile-schema/traits", todo, ok=(201,))
    print(f"Trait attributes: {len(body)} ({len(todo)} new)")


def cmd_rules():
    status, current = api("GET", "/unification-rules", ok=None)
    present = {r["rule_name"]: r for r in (current or []) if isinstance(r, dict)}
    for rule in RULES:
        if rule["rule_name"] in present:
            continue
        api("POST", "/unification-rules", dict(rule, is_active=True), ok=(201,))
    print(f"Rules: {len(RULES)} (rule set R5)")


def cmd_ruleset(name):
    if name not in RULE_SETS:
        die(f"Unknown rule set {name}. Use one of {', '.join(RULE_SETS)}.")
    active = RULE_SETS[name]
    names = ",".join(f"'{n}'" for n in active) or "''"
    psql(f"UPDATE unification_rules SET is_active = (rule_name IN ({names})), updated_at = now() "
         f"WHERE org_id = '{org_id()}' AND rule_name LIKE 'perf-%'")
    cmd_restart_cds()
    print(f"Rule set {name}: {', '.join(active) or 'no rules'}")


def cmd_decisive(value):
    api("PATCH", "/config", {"deterministic_match_decisive": value == "true"})
    print(f"deterministic_match_decisive = {value}")


def cmd_load_seed(path, threads=32):
    rows = list(csv.DictReader(open(path)))
    work = queue.Queue()
    for row in rows:
        work.put(row)
    stats = {"ok": 0, "fail": 0}
    lock = threading.Lock()
    url = urllib.parse.urlparse(CDS_BASE)
    tok = token()

    def worker():
        conn = http.client.HTTPSConnection(url.hostname, url.port or 443, context=SSL, timeout=60)
        while True:
            try:
                row = work.get_nowait()
            except queue.Empty:
                break
            # POST /profiles takes no user_id. A permanent seed profile gets its user ID
            # through POST /profiles/link, as an application does.
            headers = {"Authorization": f"Bearer {tok}", "Content-Type": "application/json"}
            if row["kind"] == "permanent":
                body = {"traits": {
                    k: row[k] for k in ("email", "phone", "nic", "first_name", "last_name",
                                        "address", "alt_email", "alt_name")}}
            else:
                body = {"traits": {"last_page": "/home", "page_views": 1}}
            try:
                conn.request("POST", f"{API}/profiles", json.dumps(body), headers)
                resp = conn.getresponse()
                raw = resp.read()
                good = resp.status in (200, 201)
                if good and row["kind"] == "permanent":
                    profile_id = json.loads(raw)["profile_id"]
                    conn.request("POST", f"{API}/profiles/link", json.dumps(
                        {"profile_id": profile_id, "user_id": row["user_id"]}), headers)
                    resp = conn.getresponse()
                    resp.read()
                    good = resp.status in (200, 201)
            except (OSError, http.client.HTTPException, ValueError, KeyError):
                conn.close()
                conn = http.client.HTTPSConnection(url.hostname, url.port or 443, context=SSL,
                                                   timeout=60)
                good = False
            with lock:
                stats["ok" if good else "fail"] += 1
                done = stats["ok"] + stats["fail"]
            if done % 10000 == 0:
                print(f"  {done} of {len(rows)} ({stats['fail']} failed)", flush=True)

    start = time.time()
    pool = [threading.Thread(target=worker) for _ in range(threads)]
    for t in pool:
        t.start()
    for t in pool:
        t.join()
    took = time.time() - start
    print(f"Seed: {stats['ok']} profiles in {took:.0f} s ({stats['ok'] / took:.0f}/s), "
          f"{stats['fail']} failed")
    if stats["fail"] > len(rows) * 0.001:
        die("More than 0.1 % of the seed requests failed.")


def queue_size():
    user, password = env("BROKER_USER"), env("BROKER_PASSWORD")
    host = os.environ.get("ACTIVEMQ_HOST", "activemq")
    mbean = ("org.apache.activemq:type=Broker,brokerName=localhost,destinationType=Queue,"
             "destinationName=cds-profile-unification")
    req = urllib.request.Request(f"http://{host}:8161/api/jolokia/read/{mbean}/QueueSize")
    req.add_header("Authorization", "Basic " + base64.b64encode(f"{user}:{password}".encode()).decode())
    req.add_header("Origin", "http://localhost")
    with urllib.request.urlopen(req, timeout=10) as resp:
        return int(json.load(resp)["value"])


def cmd_wait_queue(timeout=7200):
    start = time.time()
    empty_since = None
    while time.time() - start < timeout:
        try:
            size = queue_size()
        except Exception as err:  # The broker can be busy. Try again.
            print(f"  queue read failed: {err}", flush=True)
            size = -1
        if size == 0:
            empty_since = empty_since or time.time()
            if time.time() - empty_since >= 30:
                print(f"The queue is empty (waited {time.time() - start:.0f} s)")
                return
        else:
            empty_since = None
            if size > 0 and int(time.time() - start) % 60 < 10:
                print(f"  queue size {size}", flush=True)
        time.sleep(10)
    die(f"The queue is not empty after {timeout} s.")


def pg_env():
    return dict(os.environ, PGPASSWORD=env("CDS_DB_PASS"), PGHOST=env("CDS_DB_HOST", "localhost"),
                PGUSER=env("CDS_DB_USER", "postgres"))


def cmd_snapshot(path):
    start = time.time()
    subprocess.run(["pg_dump", "-Fc", "-Z", "1", "-d", env("CDS_DB_NAME", "cds_db"), "-f", path],
                   env=pg_env(), check=True)
    print(f"Snapshot {path} ({os.path.getsize(path) // 1048576} MB) in {time.time() - start:.0f} s")


def set_cds(action):
    for host in cds_nodes():
        out = node_ssh(host, f"sudo systemctl {action} cds")
        if out.returncode != 0:
            die(f"systemctl {action} cds on {host}: {out.stderr.strip()}")


def wait_ready(timeout=180):
    for host in cds_nodes():
        start = time.time()
        while True:
            try:
                req = urllib.request.Request(f"https://{host}:8900/cds/api/v1/ready")
                with urllib.request.urlopen(req, context=SSL, timeout=5) as resp:
                    if resp.status == 200:
                        break
            except Exception:
                pass
            if time.time() - start > timeout:
                die(f"CDS on {host} is not ready after {timeout} s.")
            time.sleep(2)


def cmd_restart_cds():
    if not cds_nodes():
        print("No CDS nodes to restart (CDS_NODES is empty).")
        return
    set_cds("restart")
    wait_ready()
    # A short pause, so that the first requests do not meet cold connections.
    time.sleep(5)
    print(f"Restarted CDS on {' '.join(cds_nodes())}")


def purge_queue():
    """Removes all jobs from the unification queue. Returns False if that is not possible."""
    if not cds_nodes():
        return False
    out = node_ssh("activemq", "sudo -u activemq /opt/activemq/bin/activemq purge "
                               "cds-profile-unification >/dev/null 2>&1; echo done")
    if "done" not in out.stdout:
        print(f"WARNING: cannot clear the queue: {out.stderr.strip()}", file=sys.stderr)
        return False
    return True


def cmd_purge_queue():
    # The jobs of the earlier scenario are not part of the next test. Without
    # this, a scenario that ends with a backlog (for example T3) slows the
    # worker in the next scenario (for example the time to merge of T4).
    if purge_queue():
        print("Cleared the unification queue.")
    else:
        print("The unification queue was not cleared (no CDS nodes or no access).")


def cmd_pg_stats(path, target="cds"):
    # The runner saves the statistics before and after each level. The values add up for the
    # life of the database, so the difference is the work of that level.
    if target == "is":
        # One server has all IS databases. The same statement in two databases has the same
        # queryid, so the key has the database too, and the text names the database.
        out = psql("SELECT s.dbid || ':' || s.queryid AS queryid, s.calls,"
                   " round(s.total_exec_time::numeric, 3) AS total_ms, s.rows,"
                   " s.shared_blks_hit, s.shared_blks_read,"
                   " '[' || d.datname || '] ' ||"
                   " left(regexp_replace(s.query, '\\s+', ' ', 'g'), 300) AS query"
                   " FROM pg_stat_statements s JOIN pg_database d ON d.oid = s.dbid"
                   " WHERE s.queryid IS NOT NULL", csv_out=True, target="is")
    else:
        out = psql("SELECT queryid, calls, round(total_exec_time::numeric, 3) AS total_ms, rows,"
                   " shared_blks_hit, shared_blks_read,"
                   " left(regexp_replace(query, '\\s+', ' ', 'g'), 300) AS query"
                   " FROM pg_stat_statements WHERE queryid IS NOT NULL", csv_out=True)
    # psql -t leaves out the header line.
    with open(path, "w") as h:
        h.write("queryid,calls,total_ms,rows,shared_blks_hit,shared_blks_read,query\n")
        if out:
            h.write(out + "\n")


def cmd_restore(path):
    if not os.path.exists(path):
        die(f"No snapshot {path}")
    start = time.time()
    db = env("CDS_DB_NAME", "cds_db")
    set_cds("stop")
    # The queue can have jobs for profiles that the restore removes.
    purge_queue()
    e = pg_env()
    subprocess.run(["dropdb", "--if-exists", "--force", "--maintenance-db=postgres", db], env=e, check=True)
    subprocess.run(["createdb", "--maintenance-db=postgres", db], env=e, check=True)
    # A managed database refuses some statements of the dump (for example the owner of
    # an extension). pg_restore then exits with 1 and restores the rest. The count
    # below checks that the data is there.
    subprocess.run(["pg_restore", "-j", "4", "--no-owner", "--no-privileges", "-d", db, path], env=e)
    profiles = subprocess.run(["psql", "-qAt", "-d", db, "-c", "SELECT count(*) FROM profiles"],
                              env=e, capture_output=True, text=True).stdout.strip()
    if not profiles.isdigit() or int(profiles) == 0:
        die(f"The restore of {path} has no profiles.")
    subprocess.run(["psql", "-qAt", "-d", db, "-c", "ANALYZE"], env=e, check=True)
    set_cds("start")
    wait_ready()
    print(f"Restored {path} in {time.time() - start:.0f} s")


def cmd_counts(start, end=None):
    oid = org_id()
    end = end or int(time.time())
    window = (f"p.org_id = '{oid}' AND p.created_at >= to_timestamp({int(start)}) "
              f"AND p.created_at < to_timestamp({int(end)})")
    row = psql(f"""
        SELECT count(*),
               count(*) FILTER (WHERE r.profile_status = 'MERGED_TO'),
               count(*) FILTER (WHERE EXISTS (SELECT 1 FROM review_tasks t
                                WHERE t.incoming_profile_id = p.profile_id
                                   OR t.candidate_profile_id = p.profile_id)),
               (SELECT count(*) FROM review_tasks t WHERE t.org_id = '{oid}'
                   AND t.created_at >= to_timestamp({int(start)})
                   AND t.created_at < to_timestamp({int(end)}))
          FROM profiles p JOIN profile_reference r ON r.profile_id = p.profile_id
         WHERE {window} AND p.list_profile""")
    total, merged, in_review, tasks = (int(x or 0) for x in row.split("|"))
    print(json.dumps({"window_start": int(start), "window_end": int(end), "new_profiles": total,
                      "merged": merged, "in_review_task": in_review, "review_tasks": tasks,
                      "no_action": total - merged - in_review}))


def rule_id(name):
    rules = api("GET", "/unification-rules", ok=None)[1] or []
    for rule in rules if isinstance(rules, list) else []:
        if rule.get("rule_name") == name:
            return rule.get("rule_id")
    return None


def log_marks():
    return {h: node_ssh(h, "sudo wc -l </var/log/cds.log").stdout.strip() or "0" for h in cds_nodes()}


def aborted_since(marks):
    found = False
    for host, mark in marks.items():
        out = node_ssh(host, f"sudo tail -n +{int(mark) + 1} /var/log/cds.log | grep -c 'ABORTED' || true")
        found = found or (out.stdout.strip() not in ("", "0"))
    return found


def cmd_deactivate(rule_name, prop):
    oid = org_id()
    rid = rule_id(rule_name)
    if not rid:
        die(f"No rule {rule_name}")
    count_sql = (f"SELECT count(*) FROM blocking_keys WHERE org_id = '{oid}' "
                 f"AND attribute_name = '{prop}'")
    keys_before = int(psql(count_sql))
    marks = log_marks()
    start = time.time()
    api("PATCH", f"/unification-rules/{rid}", {"is_active": False})
    keys = keys_before
    while time.time() - start < 600:
        keys = int(psql(count_sql))
        if keys == 0:
            break
        time.sleep(1)
    print(json.dumps({"rule": rule_name, "property": prop, "keys_before": keys_before,
                      "keys_after": keys, "seconds": round(time.time() - start, 1),
                      "complete": keys == 0, "aborted_in_log": aborted_since(marks)}))


def cmd_reindex(rule_name, prop, attr_type, method):
    oid = org_id()
    old = rule_id(rule_name)
    if old:
        api("DELETE", f"/unification-rules/{old}")
        time.sleep(35)  # The rules cache keeps a value for 30 seconds.
    profiles = int(psql(f"SELECT count(*) FROM profiles WHERE org_id = '{oid}' "
                        f"AND NOT delete_profile AND traits ? '{prop.split('.', 1)[1]}'"))
    psql(f"DELETE FROM blocking_keys WHERE org_id = '{oid}' AND attribute_name = '{prop}'")
    priority = 1 + int(psql(f"SELECT coalesce(max(priority), 0) FROM unification_rules "
                            f"WHERE org_id = '{oid}'"))
    marks = log_marks()
    start = time.time()
    api("POST", "/unification-rules", {"rule_name": rule_name, "property_name": prop,
                                        "priority": priority, "is_active": True,
                                        "attribute_type": attr_type,
                                        "unification_method": method}, ok=(201,))
    count_sql = (f"SELECT count(DISTINCT profile_id) FROM blocking_keys "
                 f"WHERE org_id = '{oid}' AND attribute_name = '{prop}'")
    last, last_change, indexed = -1, time.time(), 0
    while True:
        indexed = int(psql(count_sql))
        now = time.time()
        if indexed != last:
            last, last_change = indexed, now
        if indexed >= profiles or now - last_change >= 60:
            break
        time.sleep(2)
    took = last_change - start
    aborted = aborted_since(marks)
    print(json.dumps({"rule": rule_name, "property": prop, "attribute_type": attr_type,
                      "method": method, "profiles_with_value": profiles,
                      "profiles_indexed": indexed, "seconds": round(took, 1),
                      "profiles_per_sec": round(indexed / took, 1) if took > 0 else None,
                      "complete": indexed >= profiles, "aborted_in_log": aborted}))


def main(argv):
    if not argv:
        print(__doc__, file=sys.stderr)
        return 2
    cmd, args = argv[0], argv[1:]
    table = {
        "schema": (cmd_schema, 0), "rules": (cmd_rules, 0), "ruleset": (cmd_ruleset, 1),
        "decisive": (cmd_decisive, 1), "load-seed": (cmd_load_seed, 1),
        "wait-queue": (cmd_wait_queue, 0), "snapshot": (cmd_snapshot, 1),
        "restore": (cmd_restore, 1), "restart-cds": (cmd_restart_cds, 0),
        "counts": (cmd_counts, 1), "reindex": (cmd_reindex, 4),
        "deactivate": (cmd_deactivate, 2), "purge-queue": (cmd_purge_queue, 0),
        "pg-stats": (cmd_pg_stats, 1),
        "pg-stats-is": (lambda path: cmd_pg_stats(path, "is"), 1),
    }
    if cmd not in table:
        print(__doc__, file=sys.stderr)
        return 2
    fn, nargs = table[cmd]
    if cmd == "counts" and len(args) in (1, 2):
        fn(*[int(a) for a in args])
    elif len(args) != nargs:
        print(__doc__, file=sys.stderr)
        return 2
    else:
        fn(*args)
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
