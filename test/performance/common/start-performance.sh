#!/usr/bin/env bash
# shellcheck source-path=SCRIPTDIR
# Creates the AWS deployment for the CDS performance tests, sets up all nodes,
# runs the tests, downloads the results, and deletes the deployment.
# The structure follows https://github.com/wso2/performance-is
# (common/start-performance.sh). The pattern folders (single-node,
# two-node-cluster, three-node-cluster) call this script with -g.
#
# Runs on the operator machine. Works with bash 3.2 (macOS).
#
# WARNING: this script creates AWS resources that cost money. Use -p first.
# It shows the create command, and creates nothing.
#
# Manual mode (-O): you create the stack in the AWS console and write its
# outputs in a file. Then the script makes no AWS call. It only sets up the
# nodes over SSH, runs the tests, and downloads the results. You delete the
# stack in the AWS console. See test/performance/README.md.

set -euo pipefail

perf_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
repo_dir="$(cd "$perf_dir/../.." && pwd)"
common_dir="$perf_dir/common"
template="$common_dir/cloudformation/cds-performance.yaml"
source "$common_dir/common-functions.sh"

timestamp="$(date +%Y%m%d-%H%M%S)"
script_start_time=$(date +%s)

key_file=""
cds_src=""
key_name=""
is_pack=""
ext_dir=""
cds_binary=""
no_of_nodes=""
is_nodes=1
ssh_cidr=""
user_tag=""
run_dir=""
cds_db_snapshot_id=""
cds_instance_type="c6i.large"
is_instance_type="c6i.xlarge"
nginx_instance_type="c6i.xlarge"
activemq_instance_type="c6i.large"
bastion_instance_type="c6i.xlarge"
cds_db_instance_type="db.m6i.xlarge"
is_db_instance_type="db.m6i.large"
cds_db_storage=100
deployment_size="performance"
seed_count=100000
is_heap="4g"
setup_only=false
detach=false
collect_only=false
keep_stack=false
assume_yes=false
dry_run=false
perf_args=()
deployment_file=""

usage() {
    cat <<EOF
Usage: $0 -g <nodes> -k <key.pem> -n <is.zip> -x <extensions dir> -b <cds binary>
          -a <ssh cidr> -q <user tag> [options] [-- <perf-test-cds.sh options>]
       $0 -g <nodes> -k <key.pem> -n <is.zip> -x <extensions dir> -b <cds binary>
          -O <deployment file> [-o <dir>] [-T] [-- <perf-test-cds.sh options>]

Required:
  -g <1|2|3>     Number of CDS nodes. The pattern folders set this value.
  -k <file>      The private key of the EC2 key pair. The key pair name is the
                 file name without .pem, unless -K gives it.
  -n <file>      The WSO2 Identity Server pack (zip).
  -x <dir>       The directory with the four CDS extension bundles for IS
                 (org.wso2.identity.customer.data.service.*.jar). It can be a
                 build tree: the script searches it. Not needed for an IS pack
                 that has the CDS bundles. Such a pack uses its own bundles.
  -b <file>      The CDS binary for linux/amd64. Build it from the CDS commit that
                 you want to test:
                   GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o cds-linux-amd64 ./cmd/server
  -c <dir>       The CDS source checkout of the binary. The database schema
                 and the CDS configuration come from it, so they match the
                 binary. Default: this checkout. The script stops if the commit
                 of the checkout is not the commit of the binary.
  -a <cidr>      The CIDR that can open SSH to the bastion node, for example
                 the public IP of this machine with /32.
  -q <tag>       The value of the User tag on all resources.

Manual mode (a stack that you created in the AWS console):
  -O <file>      The outputs of the stack and the two database passwords, in
                 the form of common/deployment/manual-deployment.env.example.
                 The script makes no AWS call, and -a, -q, -K, -s, -C to -S,
                 -P, -y, and -p do not apply. Delete the stack in the console.

Options:
  -i <1|2>       Number of IS nodes. With 2, IS runs as a cluster. The pattern
                 folders set a default: 1 for single-node, 2 for the others.
                 In manual mode, it must match the ISNodeCount of the stack.
  -K <name>      The EC2 key pair name. Default: the key file name without .pem.
  -o <dir>       The run directory for the results, the certificates, and the
                 secrets of this run. Default: ./aws-run-<timestamp>.
  -s <id>        A CDS database snapshot with a loaded data set.
  -C <type>      CDS instance type. Default: $cds_instance_type.
  -I <type>      IS instance type. Default: $is_instance_type.
  -N <type>      nginx instance type. Default: $nginx_instance_type.
  -M <type>      ActiveMQ instance type. Default: $activemq_instance_type.
  -B <type>      Bastion (JMeter) instance type. Default: $bastion_instance_type.
  -E <type>      CDS RDS instance class. Default: $cds_db_instance_type.
  -F <type>      IS RDS instance class. Default: $is_db_instance_type.
  -S <GB>        CDS RDS storage. Default: $cds_db_storage.
  -H <heap>      IS heap. Default: $is_heap.
  -z             Smoke size: small burstable instances for an end-to-end
                 check of the wiring. It ignores -C to
                 -S. Without options after --, the test is short:
                 -c "5 20" -d 2 -w 0.5. In manual mode, write
                 DeploymentSize=smoke in the deployment file in place of -z.
  -T             Set up only. Do not run the tests, and keep the stack.
  -D             Set up, start the tests on the bastion node, and exit. The
                 tests continue without this machine. Collect the results
                 later with -R. Keeps the stack.
  -R             Manual mode only. Collect the results of the tests that -D
                 started: wait until they are done, then download the
                 results and the logs of all nodes. No setup. -n, -x, -b, and
                 -c are not necessary.
  -P             Keep the stack after the tests. Delete it later with
                 common/stop-performance.sh.
  -y             Do not ask for confirmation before the stack is created.
  -p             Dry run. Check the inputs, validate the template, and show the
                 create command. Create nothing.
  -h             Show this help.

Options after -- go to perf-test-cds.sh on the bastion node, for example
  -- -c "50 100 150 300 500" -d 15 -w 5 -i anonymous_visitor
EOF
}

while getopts "g:i:k:n:x:b:c:a:q:K:o:s:C:I:N:M:B:E:F:S:H:O:zTDRPyph" opt; do
    case "$opt" in
        g) no_of_nodes="$OPTARG" ;;
        i) is_nodes="$OPTARG" ;;
        k) key_file="$OPTARG" ;;
        n) is_pack="$OPTARG" ;;
        x) ext_dir="$OPTARG" ;;
        b) cds_binary="$OPTARG" ;;
        c) cds_src="$OPTARG" ;;
        a) ssh_cidr="$OPTARG" ;;
        q) user_tag="$OPTARG" ;;
        K) key_name="$OPTARG" ;;
        o) run_dir="$OPTARG" ;;
        s) cds_db_snapshot_id="$OPTARG" ;;
        C) cds_instance_type="$OPTARG" ;;
        I) is_instance_type="$OPTARG" ;;
        N) nginx_instance_type="$OPTARG" ;;
        M) activemq_instance_type="$OPTARG" ;;
        B) bastion_instance_type="$OPTARG" ;;
        E) cds_db_instance_type="$OPTARG" ;;
        F) is_db_instance_type="$OPTARG" ;;
        S) cds_db_storage="$OPTARG" ;;
        H) is_heap="$OPTARG" ;;
        O) deployment_file="$OPTARG" ;;
        z) deployment_size="smoke" ;;
        T) setup_only=true ;;
        D) detach=true; keep_stack=true ;;
        R) collect_only=true ;;
        P) keep_stack=true ;;
        y) assume_yes=true ;;
        p) dry_run=true ;;
        h) usage; exit 0 ;;
        *) usage >&2; exit 2 ;;
    esac
done
shift $((OPTIND - 1))
[ "${1:-}" = "--" ] && shift
[ $# -gt 0 ] && perf_args=("$@")

# --------------------------------------------------------------------------- #
# Check the inputs. Nothing is created before all checks pass.
# --------------------------------------------------------------------------- #
manual=false
[ -z "$deployment_file" ] || manual=true
for c in jq ssh scp openssl awk zip unzip; do check_command "$c"; done
$manual || check_command aws

case "$no_of_nodes" in 1 | 2 | 3) ;; *) die "-g must be 1, 2, or 3." ;; esac
case "$is_nodes" in 1 | 2) ;; *) die "-i must be 1 or 2." ;; esac
[ -f "$key_file" ] || die "Give the key file with -k."
[ "${key_file##*.}" = "pem" ] || die "The key file must have the .pem extension."
key_file="$(cd "$(dirname "$key_file")" && pwd)/$(basename "$key_file")"
[ -n "$key_name" ] || key_name="$(basename "$key_file" .pem)"
if $collect_only; then
    $manual || die "-R works only in manual mode (-O)."
else
    [ -f "$is_pack" ] || die "Give the IS pack with -n."
    # An IS pack with the CDS integration lists the CDS bundles in bundles.info.
    pack_has_cds=false
    if unzip -p "$is_pack" '*/org.eclipse.equinox.simpleconfigurator/bundles.info' 2>/dev/null \
        | grep -q '^org\.wso2\.identity\.customer\.data\.service\.client,'; then
        pack_has_cds=true
    fi
    $pack_has_cds || [ -d "$ext_dir" ] || die "Give the directory of the CDS extension bundles with -x."
    [ -f "$cds_binary" ] || die "Give the CDS binary with -b."
    [ -n "$cds_src" ] || cds_src="$repo_dir"
    [ -f "$cds_src/dbscripts/postgres.sql" ] && [ -f "$cds_src/config/repository/conf/deployment.yaml" ] \
        || die "$cds_src is not a CDS source checkout. Give the source of the binary with -c."
    cds_src="$(cd "$cds_src" && pwd)"

    # cds_build_info KEY - a value that the Go tool chain stored in the CDS binary
    # at build time: "mod" (the module version, with the commit for a build from a
    # branch), "vcs.revision" (the commit), or "vcs.modified" (uncommitted changes).
    # The version.txt of this checkout is not used, because the binary can come from
    # another branch.
    cds_build_info() {
        local out=""
        command -v go >/dev/null && out="$(go version -m "$cds_binary" 2>/dev/null || true)"
        if [ "$1" = "mod" ]; then
            printf '%s\n' "$out" | awk '$1 == "mod" { print $3; exit }'
        else
            printf '%s\n' "$out" | awk -v k="$1=" '$1 == "build" && index($2, k) == 1 { print substr($2, length(k) + 1); exit }'
        fi | grep . || echo unknown
    }
    binary_commit="$(cds_build_info vcs.revision)"
    source_commit="$(git -C "$cds_src" rev-parse HEAD 2>/dev/null || echo unknown)"
    if [ "$binary_commit" != "unknown" ] && [ "$source_commit" != "unknown" ] && [ "$binary_commit" != "$source_commit" ]; then
        die "The binary is from commit ${binary_commit:0:7}, but $cds_src is at commit ${source_commit:0:7}. Give the source of the binary with -c."
    fi
fi
if $manual; then
    [ -f "$deployment_file" ] || die "Cannot read the deployment file: $deployment_file"
else
    [[ "$ssh_cidr" =~ ^([0-9]{1,3}\.){3}[0-9]{1,3}/[0-9]{1,2}$ ]] || die "Give an IPv4 CIDR with -a, for example 203.0.113.10/32."
    [ "$ssh_cidr" != "0.0.0.0/0" ] || die "Do not open SSH to 0.0.0.0/0."
    [ -n "$user_tag" ] || die "Give the user tag with -q."
fi

if ! $collect_only; then
    if command -v file >/dev/null 2>&1; then
        file -b "$cds_binary" | grep -q 'ELF 64-bit.*x86-64' \
            || die "The CDS binary is not a linux/amd64 executable: $(file -b "$cds_binary")"
    fi
    bundles=""
    if $pack_has_cds; then
        # The pack has the CDS bundles and the CDS API resources. IS gets only
        # the short CDS section.
        is_cds_section="$common_dir/deployment/setup/resources/is-cds-section-pack.toml"
        [ -z "$ext_dir" ] || echo "The IS pack has the CDS bundles. The bundles in $ext_dir are not used."
    else
        is_cds_section="$cds_src/scripts/local-setup/templates/is-deployment.toml"
        for b in client event.handler auth.listener auth.handler; do
            jar="$(find "$ext_dir" -name "org.wso2.identity.customer.data.service.$b-*.jar" \
                ! -name '*-sources.jar' ! -name '*-javadoc.jar' | head -1)"
            [ -n "$jar" ] || die "No org.wso2.identity.customer.data.service.$b jar in $ext_dir."
            bundles="$bundles $jar"
        done
    fi
    unzip -l "$is_pack" | grep 'repository/conf/deployment.toml' >/dev/null || die "$is_pack is not an IS pack."
fi

[ -n "$run_dir" ] || run_dir="$PWD/aws-run-$timestamp"
mkdir -p "$run_dir"
run_dir="$(cd "$run_dir" && pwd)"
chmod 700 "$run_dir"
stack_name="cds-perf-${no_of_nodes}node-$timestamp"
secret() { openssl rand -hex 16; }
# The key that seals the service credentials in the CDS database (32 bytes, base64).
secret_key() { openssl rand -base64 32; }
secrets_file="$run_dir/secrets.env"

# write_secrets DB_USER CDS_DB_PASS IS_DB_PASS - writes the secrets of this run.
# The database values are the stack parameters. The other values are new. No
# value is a product default (for example admin, wso2carbon, or dashboard).
# A file from an earlier run of the same stack keeps its values, because the
# IS database has data that the encryption key protects. It only gets the keys
# that it does not have.
write_secrets() {
    if [ -f "$secrets_file" ]; then
        local name
        for name in IS_KEYSTORE_PASS IS_APP_PASS IS_ENCRYPTION_KEY; do
            grep -q "^$name=" "$secrets_file" || printf '%s=%s\n' "$name" "$(secret)" >>"$secrets_file"
        done
        grep -q '^SERVICE_CREDENTIALS_ENCRYPTION_KEY=' "$secrets_file" \
            || printf 'SERVICE_CREDENTIALS_ENCRYPTION_KEY=%s\n' "$(secret_key)" >>"$secrets_file"
        return 0
    fi
    (
        umask 077
        cat >"$secrets_file" <<EOF
DB_USER=$1
CDS_DB_PASS=$2
IS_DB_PASS=$3
IS_ADMIN_PASS=$(secret)
CDS_SYNC_USER=cdssync
CDS_SYNC_PASS=$(secret)
BROKER_USER=cds
BROKER_PASSWORD=$(secret)
IS_KEYSTORE_PASS=$(secret)
IS_APP_PASS=$(secret)
IS_ENCRYPTION_KEY=$(secret)
SERVICE_CREDENTIALS_ENCRYPTION_KEY=$(secret_key)
EOF
    )
}

# kv FILE KEY - one KEY=value line of a file, without quotes. Not sourced, so
# the file cannot run commands.
kv() {
    local line
    line="$(grep -E "^[[:space:]]*$2=" "$1" | tail -n 1 || true)"
    line="${line#*=}"
    line="${line%\"}"; line="${line#\"}"
    printf '%s' "$line"
}

# The size of the stack. The smoke values must match the IsSmoke values of the
# template.
if $manual; then
    file_size="$(kv "$deployment_file" DeploymentSize)"
    [ -z "$file_size" ] || deployment_size="$file_size"
fi
case "$deployment_size" in
    performance) ;;
    smoke)
        cds_instance_type="t3.small"
        nginx_instance_type="t3.small"
        activemq_instance_type="t3.medium"
        is_instance_type="t3.large"
        bastion_instance_type="t3.medium"
        cds_db_instance_type="db.t4g.small"
        is_db_instance_type="db.t4g.small"
        cds_db_storage=20
        [ ${#perf_args[@]} -gt 0 ] || perf_args=(-c "5 20" -t "5 20 drain" -d 2 -w 0.5)
        seed_count=2000
        ;;
    *) die "DeploymentSize must be performance or smoke." ;;
esac

if $manual; then
    # The group and the other users must not read or write the file.
    if [ -n "$(find "$deployment_file" \( -perm -040 -o -perm -020 -o -perm -004 -o -perm -002 \) -print)" ]; then
        die "The deployment file has passwords. Run: chmod 600 $deployment_file"
    fi
    stack_name="$(kv "$deployment_file" STACK_NAME)"
    bastion_ip="$(kv "$deployment_file" BastionPublicIp)"
    nginx_ip="$(kv "$deployment_file" NginxPrivateIp)"
    is_ip="$(kv "$deployment_file" ISPrivateIp)"
    is_ips=("$is_ip")
    [ "$is_nodes" -ge 2 ] && is_ips+=("$(kv "$deployment_file" ISNode2PrivateIp)")
    activemq_ip="$(kv "$deployment_file" ActiveMQPrivateIp)"
    cds_db_host="$(kv "$deployment_file" CDSDBEndpoint)"
    is_db_host="$(kv "$deployment_file" ISDBEndpoint)"
    cds_ips=("$(kv "$deployment_file" CDSNode1PrivateIp)")
    [ "$no_of_nodes" -ge 2 ] && cds_ips+=("$(kv "$deployment_file" CDSNode2PrivateIp)")
    [ "$no_of_nodes" -ge 3 ] && cds_ips+=("$(kv "$deployment_file" CDSNode3PrivateIp)")
    case "$bastion_ip" in
        10.* | 192.168.* | 172.1[6-9].* | 172.2[0-9].* | 172.3[01].*)
            die "BastionPublicIp $bastion_ip is a private IP. Copy the BastionPublicIp output, not BastionPrivateIp." ;;
    esac
    for value in "$stack_name" "$bastion_ip" "$nginx_ip" "$activemq_ip" "$cds_db_host" "$is_db_host" "${is_ips[@]}" "${cds_ips[@]}"; do
        [ -n "$value" ] || die "A value is missing in $deployment_file. Compare it with manual-deployment.env.example and the -g and -i values."
    done
    [ -n "$(kv "$deployment_file" CDSDBPassword)" ] && [ -n "$(kv "$deployment_file" ISDBPassword)" ] \
        || die "Give CDSDBPassword and ISDBPassword in $deployment_file."
    write_secrets "$(kv "$deployment_file" DBUsername)" \
        "$(kv "$deployment_file" CDSDBPassword)" "$(kv "$deployment_file" ISDBPassword)"
    # shellcheck source=/dev/null
    source "$secrets_file"
    [ -n "$DB_USER" ] || die "Give DBUsername in $deployment_file."
    echo "$stack_name" >"$run_dir/stack-name.txt"
    log "Manual mode: stack $stack_name, $no_of_nodes CDS node(s), $is_nodes IS node(s), bastion $bastion_ip"
    cleanup() {
        local rc=$?
        log "The stack $stack_name stays. Delete it in the AWS console when you finish."
        log "Total time: $(format_time "$(measure_time "$script_start_time")")"
        exit "$rc"
    }
    trap cleanup EXIT
    trap 'exit 130' INT TERM
fi

if ! $manual; then
# --------------------------------------------------------------------------- #
# Summary of the stack
# --------------------------------------------------------------------------- #
scenario_count="$("$common_dir/jmeter/perf-test-cds.sh" -l | wc -l | tr -d ' ')"

echo "Stack:            $stack_name"
echo "AWS region:       $(aws configure get region 2>/dev/null || echo "${AWS_REGION:-not set}")"
echo "AWS profile:      ${AWS_PROFILE:-default}"
echo "Size:             $deployment_size"
echo "CDS nodes:        $no_of_nodes x $cds_instance_type"
echo "IS nodes:         $is_nodes x $is_instance_type"
echo "Other nodes:      nginx $nginx_instance_type, ActiveMQ $activemq_instance_type, bastion $bastion_instance_type"
echo "Databases:        CDS $cds_db_instance_type (${cds_db_storage} GB), IS $is_db_instance_type"
echo "CDS snapshot:     ${cds_db_snapshot_id:-none (empty database)}"
echo "Scenarios:        $scenario_count, options: ${perf_args[*]:-defaults of perf-test-cds.sh}"
echo "Run directory:    $run_dir"
echo

create_params=(
    "ParameterKey=DeploymentSize,ParameterValue=$deployment_size"
    "ParameterKey=KeyPairName,ParameterValue=$key_name"
    "ParameterKey=AllowedSshCidr,ParameterValue=$ssh_cidr"
    "ParameterKey=UserTag,ParameterValue=$user_tag"
    "ParameterKey=CDSNodeCount,ParameterValue=$no_of_nodes"
    "ParameterKey=ISNodeCount,ParameterValue=$is_nodes"
    "ParameterKey=DBUsername,ParameterValue=perfadmin"
)
# A smoke stack sets its own sizes and ignores these parameters, and their
# allowed values do not include the smoke sizes. So keep the template defaults.
if [ "$deployment_size" = "performance" ]; then
    create_params+=(
        "ParameterKey=CDSInstanceType,ParameterValue=$cds_instance_type"
        "ParameterKey=NginxInstanceType,ParameterValue=$nginx_instance_type"
        "ParameterKey=ActiveMQInstanceType,ParameterValue=$activemq_instance_type"
        "ParameterKey=ISInstanceType,ParameterValue=$is_instance_type"
        "ParameterKey=BastionInstanceType,ParameterValue=$bastion_instance_type"
        "ParameterKey=CDSDBInstanceType,ParameterValue=$cds_db_instance_type"
        "ParameterKey=CDSDBStorageGB,ParameterValue=$cds_db_storage"
        "ParameterKey=ISDBInstanceType,ParameterValue=$is_db_instance_type"
    )
fi
# The template default (empty) means no snapshot.
[ -z "$cds_db_snapshot_id" ] || create_params+=("ParameterKey=CDSDBSnapshotId,ParameterValue=$cds_db_snapshot_id")

log "Validate the template"
aws cloudformation validate-template --template-body "file://$template" >/dev/null
aws ec2 describe-key-pairs --key-names "$key_name" >/dev/null \
    || die "The key pair $key_name does not exist in this region."

if $dry_run; then
    echo
    echo "Dry run. The create command is (the passwords are new for each run):"
    echo "aws cloudformation create-stack --stack-name $stack_name --template-body file://$template \\"
    printf '    --parameters'
    printf ' %s' "${create_params[@]}"
    printf ' ParameterKey=CDSDBPassword,ParameterValue=*** ParameterKey=ISDBPassword,ParameterValue=***\n'
    exit 0
fi

if ! $assume_yes; then
    printf 'This creates AWS resources that cost money. Type "yes" to continue: '
    read -r answer
    [ "$answer" = "yes" ] || die "Stopped. Nothing was created."
fi

# --------------------------------------------------------------------------- #
# Secrets of this run. They stay in the run directory.
# --------------------------------------------------------------------------- #
write_secrets perfadmin "$(secret)" "$(secret)"
# shellcheck source=/dev/null
source "$secrets_file"

# --------------------------------------------------------------------------- #
# Create the stack
# --------------------------------------------------------------------------- #
echo "$stack_name" >"$run_dir/stack-name.txt"
log "Create the stack $stack_name"
stack_create_start=$(date +%s)
# The parameters go in a file (mode 600), so that the passwords are not in the
# process list of this machine.
params_file="$run_dir/stack-parameters.json"
(
    umask 077
    for p in "${create_params[@]}" \
        "ParameterKey=CDSDBPassword,ParameterValue=$CDS_DB_PASS" \
        "ParameterKey=ISDBPassword,ParameterValue=$IS_DB_PASS"; do
        k="${p#ParameterKey=}"
        k="${k%%,*}"
        jq -n --arg k "$k" --arg v "${p#*,ParameterValue=}" '{ParameterKey:$k, ParameterValue:$v}'
    done | jq -s . >"$params_file"
)
aws cloudformation create-stack --stack-name "$stack_name" \
    --template-body "file://$template" \
    --tags "Key=User,Value=$user_tag" "Key=Purpose,Value=cds-performance" \
    --parameters "file://$params_file" >/dev/null

cleanup() {
    local rc=$?
    aws cloudformation describe-stack-events --stack-name "$stack_name" --output json \
        >"$run_dir/stack-events.json" 2>/dev/null || true
    if $keep_stack || $setup_only; then
        log "The stack $stack_name stays. Delete it with:"
        log "  $common_dir/stop-performance.sh $stack_name"
    else
        delete_stack "$stack_name"
    fi
    log "Total time: $(format_time "$(measure_time "$script_start_time")")"
    exit "$rc"
}
trap cleanup EXIT
trap 'exit 130' INT TERM

log "Wait for the stack (about 15 minutes, the RDS instances take the longest)"
aws cloudformation wait stack-create-complete --stack-name "$stack_name" \
    || die "The stack was not created. See $run_dir/stack-events.json."
log "Stack created in $(format_time "$(measure_time "$stack_create_start")")"

out() { stack_output "$stack_name" "$1"; }
bastion_ip="$(out BastionPublicIp)"
nginx_ip="$(out NginxPrivateIp)"
is_ip="$(out ISPrivateIp)"
is_ips=("$is_ip")
[ "$is_nodes" -ge 2 ] && is_ips+=("$(out ISNode2PrivateIp)")
activemq_ip="$(out ActiveMQPrivateIp)"
cds_db_host="$(out CDSDBEndpoint)"
is_db_host="$(out ISDBEndpoint)"
cds_ips=("$(out CDSNode1PrivateIp)")
[ "$no_of_nodes" -ge 2 ] && cds_ips+=("$(out CDSNode2PrivateIp)")
[ "$no_of_nodes" -ge 3 ] && cds_ips+=("$(out CDSNode3PrivateIp)")
for value in "$bastion_ip" "$nginx_ip" "$activemq_ip" "$cds_db_host" "$is_db_host" "${is_ips[@]}" "${cds_ips[@]}"; do
    case "$value" in
        "" | None) die "A stack output is empty. See: aws cloudformation describe-stacks --stack-name $stack_name" ;;
    esac
done
fi # end of the AWS part (not in manual mode)

cat >"$run_dir/stack.env" <<EOF
STACK_NAME=$stack_name
BASTION_IP=$bastion_ip
NGINX_IP=$nginx_ip
IS_IPS="${is_ips[*]}"
ACTIVEMQ_IP=$activemq_ip
CDS_IPS="${cds_ips[*]}"
CDS_DB_HOST=$cds_db_host
IS_DB_HOST=$is_db_host
EOF
write_ssh_config "$key_file" "$bastion_ip"
log "Bastion $bastion_ip. SSH with: ssh -F $run_dir/ssh_config bastion"

# wait_for_tests - waits until the test runner on the bastion node writes
# run.done, and shows the progress each minute.
wait_for_tests() {
    local start
    start=$(date +%s)
    if ! ssh_node bastion 'test -f ~/cds-perf/run.done || pgrep -f run-performance-tests.sh >/dev/null'; then
        die "No tests run on the bastion node, and no results exist. Start the tests with -D."
    fi
    until ssh_node bastion 'test -f ~/cds-perf/run.done' 2>/dev/null; do
        ssh_node bastion 'grep "^# " ~/cds-perf/results/perf-test-cds.out 2>/dev/null | tail -1' 2>/dev/null || true
        sleep 60
    done
    test_rc="$(ssh_node bastion 'cat ~/cds-perf/run.done')"
    log "Tests done (waited $(format_time "$(measure_time "$start")")) with exit code $test_rc"
}

# download_results - copies the results and the logs of all nodes to the run
# directory.
download_results() {
    local host
    log "Download the results"
    scp_from bastion "cds-perf/results.zip" "$run_dir/results.zip"
    (cd "$run_dir" && unzip -qo results.zip)
    mkdir -p "$run_dir/nodes"
    # The queue statistics of the broker (messages in and out of each queue).
    # shellcheck disable=SC2016  # The node expands these values.
    ssh_node "$activemq_ip" 'sudo -u activemq /opt/activemq/bin/activemq dstat 2>/dev/null \
        | sed -n "/^Name /,\$p" | sudo tee /var/log/activemq-dstat.txt >/dev/null' || true
    for host in "${is_ips[@]}" "$activemq_ip" "$nginx_ip" "${cds_ips[@]}"; do
        # shellcheck disable=SC2016  # The node expands these values.
        ssh_node "$host" 'sudo tar -czf /tmp/node-logs.tgz -C / var/log/sysstat $(test -f /var/log/cds.log && echo var/log/cds.log) \
            $(test -f /var/log/activemq-dstat.txt && echo var/log/activemq-dstat.txt) \
            $(test -d /opt/activemq/data && echo opt/activemq/data/activemq.log opt/activemq/data/audit.log) \
            $(test -d ~/wso2is/repository/logs && echo "${HOME#/}/wso2is/repository/logs") 2>/dev/null; sudo chmod 644 /tmp/node-logs.tgz' || true
        scp_from "$host" /tmp/node-logs.tgz "$run_dir/nodes/$host.tgz" || true
    done
    log "Results: $run_dir/results/summary.csv"
    column -s, -t "$run_dir/results/summary.csv" 2>/dev/null || true
}

if $collect_only; then
    wait_for_tests
    download_results
    exit 0
fi

# --------------------------------------------------------------------------- #
# Host names and certificates
# --------------------------------------------------------------------------- #
# wso2is and cdslb are the nginx node, which balances the IS nodes (is1, is2)
# and the CDS nodes (cds1 to cds3). The one node patterns use the same path.
{
    echo "$nginx_ip wso2is # cds-perf"
    echo "$nginx_ip cdslb # cds-perf"
    echo "$activemq_ip activemq # cds-perf"
    i=1
    for ip in "${is_ips[@]}"; do
        echo "$ip is$i # cds-perf"
        i=$((i + 1))
    done
    i=1
    for ip in "${cds_ips[@]}"; do
        echo "$ip cds$i # cds-perf"
        i=$((i + 1))
    done
} >"$run_dir/hosts"

certs="$run_dir/certs"
mkdir -p "$certs"
make_cert() {
    local name="$1" san="$2"
    openssl req -new -newkey rsa:2048 -nodes -keyout "$certs/$name.key" -out "$certs/$name.csr" \
        -subj "/CN=$name/O=CDS performance test" 2>/dev/null
    printf 'basicConstraints=CA:FALSE\nkeyUsage=digitalSignature,keyEncipherment\nextendedKeyUsage=serverAuth,clientAuth\nsubjectAltName=%s\n' \
        "$san" >"$certs/$name.ext"
    openssl x509 -req -in "$certs/$name.csr" -CA "$certs/ca.crt" -CAkey "$certs/ca.key" -CAcreateserial \
        -days 30 -sha256 -extfile "$certs/$name.ext" -out "$certs/$name.crt" 2>/dev/null
    chmod 600 "$certs/$name.key"
}
log "Make the test CA and the certificates"
(umask 077 && openssl req -x509 -newkey rsa:2048 -nodes -days 30 -sha256 \
    -keyout "$certs/ca.key" -out "$certs/ca.crt" -subj "/CN=CDS performance test CA" -addext "keyUsage=critical,keyCertSign,cRLSign" 2>/dev/null)
is_san="DNS:wso2is,DNS:localhost,IP:$nginx_ip,IP:127.0.0.1"
i=1
for ip in "${is_ips[@]}"; do
    is_san="$is_san,DNS:is$i,IP:$ip"
    i=$((i + 1))
done
make_cert is "$is_san"
cds_san="DNS:cdslb,DNS:localhost,IP:$nginx_ip,IP:127.0.0.1"
i=1
for ip in "${cds_ips[@]}"; do
    cds_san="$cds_san,DNS:cds$i,IP:$ip"
    i=$((i + 1))
done
make_cert cds "$cds_san"

# The primary key of IS signs the tokens. The pack has a default key that is
# public, so each stack gets its own key. All IS nodes of the stack use the same
# key. A new setup of the same stack keeps the key of the earlier setup.
if [ ! -f "$certs/is-primary.key" ]; then
    (umask 077 && openssl req -x509 -newkey rsa:2048 -nodes -days 365 -sha256 \
        -keyout "$certs/is-primary.key" -out "$certs/is-primary.crt" \
        -subj "/CN=wso2is/O=CDS performance test" 2>/dev/null)
fi

# The control key lets the test runner on the bastion node restart CDS and read
# the node state between the test cases (for example after a database restore,
# or to change the fuzzy switch). Only the nodes of this run trust it.
ctl_key="$certs/bastion-ctl"
[ -f "$ctl_key" ] || ssh-keygen -q -t ed25519 -N '' -C "cds-perf-bastion-$stack_name" -f "$ctl_key"

# --------------------------------------------------------------------------- #
# Node setup
# --------------------------------------------------------------------------- #
# prepare_node HOST - waits for cloud-init, copies the setup scripts and the
# host names, and sets the OS limits.
prepare_node() {
    local host="$1"
    wait_for_node "$host"
    ssh_node "$host" 'rm -rf ~/cds-perf && mkdir -p ~/cds-perf/certs && chmod 700 ~/cds-perf'
    scp_to "$host" "cds-perf/" "$common_dir/deployment/setup" "$run_dir/hosts" "$certs/ca.crt" "$ctl_key.pub"
    ssh_node "$host" 'mv ~/cds-perf/ca.crt ~/cds-perf/certs/ &&
        sudo sed -i "/# cds-perf$/d" /etc/hosts && sudo tee -a /etc/hosts <~/cds-perf/hosts >/dev/null &&
        sed -i "/cds-perf-bastion-/d" ~/.ssh/authorized_keys && cat ~/cds-perf/bastion-ctl.pub >>~/.ssh/authorized_keys &&
        ~/cds-perf/setup/tune-os.sh'
}

# send_secrets HOST NAME... - copies only the named secrets to a node.
send_secrets() {
    local host="$1" tmp name
    shift
    tmp="$run_dir/.secrets-$host.env"
    (
        umask 077
        : >"$tmp"
        for name in "$@"; do
            eval "printf '%s=%s\n' \"\$name\" \"\${$name}\"" >>"$tmp"
        done
    )
    scp_to "$host" "cds-perf/secrets.env" "$tmp"
    rm -f "$tmp"
    ssh_node "$host" 'chmod 600 ~/cds-perf/secrets.env'
}

log "Set up the bastion (JMeter) node"
prepare_node bastion
scp_to bastion ".ssh/cds-perf-ctl" "$ctl_key"
ssh_node bastion 'chmod 600 ~/.ssh/cds-perf-ctl && cat >~/.ssh/config <<EOF
Host cds1 cds2 cds3 is1 is2 activemq
    User ubuntu
    IdentityFile ~/.ssh/cds-perf-ctl
    IdentitiesOnly yes
    StrictHostKeyChecking accept-new
    ConnectTimeout 10
    LogLevel ERROR
EOF
chmod 600 ~/.ssh/config'
# send_secrets reads these names with eval.
# shellcheck disable=SC2034
CDS_DB_HOST="$cds_db_host"
# shellcheck disable=SC2034
CDS_DB_USER="$DB_USER"
send_secrets bastion CDS_DB_HOST CDS_DB_USER CDS_DB_PASS IS_ADMIN_PASS CDS_SYNC_USER CDS_SYNC_PASS BROKER_USER BROKER_PASSWORD
ssh_node bastion 'mkdir -p ~/cds-perf/perf/common ~/cds-perf/dbscripts'
scp_to bastion "cds-perf/perf/common/" "$common_dir/jmeter" "$common_dir/tools"
scp_to bastion "cds-perf/dbscripts/" "$cds_src/dbscripts/postgres.sql"
scp_to bastion "cds-perf/" "$common_dir/deployment/run-performance-tests.sh"
ssh_node bastion "cds-perf/setup/setup-bastion.sh -c $cds_db_host"

log "Set up the ActiveMQ node"
prepare_node "$activemq_ip"
send_secrets "$activemq_ip" BROKER_USER BROKER_PASSWORD
ssh_node "$activemq_ip" 'cds-perf/setup/setup-activemq.sh'

# shellcheck disable=SC2034  # send_secrets reads it with eval.
IS_DB_USER="$DB_USER"
# With 2 IS nodes, setup-is.sh gets the IPs of all members (-k).
cluster_opt=""
[ "$is_nodes" -lt 2 ] || cluster_opt=" -k $(printf '%q' "${is_ips[*]}")"
i=1
for ip in "${is_ips[@]}"; do
    # The nodes start one after the other. The first node creates the IS tables.
    log "Set up IS node $i ($ip). The upload of the IS pack can take some minutes."
    prepare_node "$ip"
    send_secrets "$ip" IS_DB_USER IS_DB_PASS IS_ADMIN_PASS CDS_SYNC_USER CDS_SYNC_PASS \
        IS_KEYSTORE_PASS IS_APP_PASS IS_ENCRYPTION_KEY
    if [ -n "$bundles" ]; then
        ssh_node "$ip" 'mkdir -p ~/cds-perf/extensions'
        # shellcheck disable=SC2086  # One argument for each jar.
        scp_to "$ip" "cds-perf/extensions/" $bundles
    fi
    scp_to "$ip" "cds-perf/certs/" "$certs/is.crt" "$certs/is.key" "$certs/is-primary.crt" "$certs/is-primary.key"
    scp_to "$ip" "cds-perf/setup/resources/is-cds-section.toml" "$is_cds_section"
    scp_to "$ip" "cds-perf/wso2is.zip" "$is_pack"
    ssh_node "$ip" "cds-perf/setup/setup-is.sh -i $ip -d $is_db_host -c https://cdslb -m $is_heap$cluster_opt"
    i=$((i + 1))
done

log "Set up the nginx node for IS"
prepare_node "$nginx_ip"
scp_to "$nginx_ip" "cds-perf/certs/" "$certs/is.crt" "$certs/is.key" "$certs/cds.crt" "$certs/cds.key"
ssh_node "$nginx_ip" "cds-perf/setup/setup-nginx.sh -s is -n $is_nodes"

log "Register the CDS applications in IS"
ssh_node bastion 'cds-perf/setup/provision-is.sh provision'
scp_from bastion "cds-perf/state.env" "$run_dir/state.env"
chmod 600 "$run_dir/state.env"

# A newer IS blocks Basic authentication on its APIs. setup-is.sh allowed it for
# provision-is.sh only. Remove that now, so that IS has the product default.
log "Block Basic authentication on the IS APIs again"
for ip in "${is_ips[@]}"; do
    ssh_node "$ip" 'cds-perf/setup/lock-is-basic-auth.sh'
done
state_get() { grep "^$1=" "$run_dir/state.env" | tail -1 | cut -d= -f2-; }

cds_env="$run_dir/cds.env"
(
    umask 077
    cat >"$cds_env" <<EOF
CDS_LOG_LEVEL=WARN
CDS_PORT=8900
CDS_BASE=https://cdslb
CDS_CERT_DIR=/opt/cds/certs
CDS_FUZZY_MATCHING=true
IS_HOST=wso2is
IS_PORT=9443
IS_BASE=https://wso2is:9443
CDS_SYNC_USER=$CDS_SYNC_USER
AUTH_SERVER_ADMIN_PASSWORD=$CDS_SYNC_PASS
SERVICE_CREDENTIALS_ENCRYPTION_KEY=$SERVICE_CREDENTIALS_ENCRYPTION_KEY
PG_HOST=$cds_db_host
PG_USER=$DB_USER
PG_DB=cds_db
DB_PASSWORD=$CDS_DB_PASS
BROKER_ADDR=activemq:61613
BROKER_USER=$BROKER_USER
BROKER_PASSWORD=$BROKER_PASSWORD
EOF
    # A pack with the CDS integration sends the service credentials of the
    # organization when IS enables CDS, and CDS uses them to call IS and to
    # introspect the tokens. Then CDS gets no configured client
    # (auth_server.client_* and introspection_client_*: setup-cds.sh removes
    # them), and provision-is.sh makes no system application. An older IS
    # sends none, so CDS needs the system application.
    if ! $pack_has_cds; then
        printf 'CDS_SYS_CLIENT_ID=%s\nAUTH_SERVER_CLIENT_SECRET=%s\nINTROSPECTION_CLIENT_SECRET=%s\n' \
            "$(state_get SYS_CLIENT_ID)" "$(state_get SYS_CLIENT_SECRET)" "$(state_get SYS_CLIENT_SECRET)" >>"$cds_env"
    fi
)

i=1
for ip in "${cds_ips[@]}"; do
    log "Set up CDS node $i ($ip)"
    prepare_node "$ip"
    scp_to "$ip" "cds-perf/cds" "$cds_binary"
    scp_to "$ip" "cds-perf/cds.env" "$cds_env"
    scp_to "$ip" "cds-perf/base-deployment.yaml" "$cds_src/config/repository/conf/deployment.yaml"
    scp_to "$ip" "cds-perf/certs/" "$certs/cds.crt" "$certs/cds.key"
    ssh_node "$ip" 'chmod 600 ~/cds-perf/cds.env && ~/cds-perf/setup/setup-cds.sh'
    i=$((i + 1))
done

log "Set up the nginx node for CDS"
ssh_node "$nginx_ip" "cds-perf/setup/setup-nginx.sh -s cds -n $no_of_nodes"

log "Enable CDS for the organization"
ssh_node bastion 'cds-perf/setup/provision-is.sh enable'

jq -n --arg nodes "$no_of_nodes" --arg isnodes "$is_nodes" --arg cds "$cds_instance_type" --arg is "$is_instance_type" \
    --arg nginx "$nginx_instance_type" --arg amq "$activemq_instance_type" \
    --arg bastion "$bastion_instance_type" --arg cdsdb "$cds_db_instance_type" \
    --arg isdb "$is_db_instance_type" --arg snapshot "$cds_db_snapshot_id" \
    --arg cdsversion "$(cds_build_info mod)" --arg cdscommit "$(cds_build_info vcs.revision)" \
    --arg cdsmodified "$(cds_build_info vcs.modified)" \
    --arg cdssha "$(shasum -a 256 "$cds_binary" | awk '{print $1}')" \
    --arg ispack "$(basename "$is_pack")" --arg heap "$is_heap" --arg size "$deployment_size" \
    '{deployment_size:$size, cds_nodes:$nodes, is_nodes:$isnodes, cds_instance_type:$cds, is_instance_type:$is, is_heap:$heap,
      nginx_instance_type:$nginx, activemq_instance_type:$amq, bastion_instance_type:$bastion,
      cds_db_instance_type:$cdsdb, is_db_instance_type:$isdb, cds_db_snapshot:$snapshot,
      cds_version:$cdsversion, cds_commit:$cdscommit, cds_uncommitted_changes:$cdsmodified,
      cds_binary_sha256:$cdssha, is_pack:$ispack}' >"$run_dir/test-metadata.json"
scp_to bastion "cds-perf/test-metadata.json" "$run_dir/test-metadata.json"

log "Setup done in $(format_time "$(measure_time "$script_start_time")")"
if $setup_only; then
    log "Set up only (-T). The tests did not run."
    log "Run a test by hand on the bastion node:"
    log "  ssh -F $run_dir/ssh_config bastion"
    log "  ~/cds-perf/run-performance-tests.sh -m ~/cds-perf/test-metadata.json -- -c 50 -d 2 -w 0.5"
    exit 0
fi

# --------------------------------------------------------------------------- #
# Run the tests. The runner continues on the bastion node if the SSH
# connection stops. This script then polls for the result.
# --------------------------------------------------------------------------- #
remote_args=""
for a in ${perf_args[@]+"${perf_args[@]}"}; do
    remote_args="$remote_args $(printf '%q' "$a")"
done
log "Run the tests"
ssh_node bastion "nohup ~/cds-perf/run-performance-tests.sh -m ~/cds-perf/test-metadata.json -S $seed_count --$remote_args \
    >~/cds-perf/run.log 2>&1 </dev/null &"
if $detach; then
    log "The tests run on the bastion node. You can close this machine."
    log "See the progress:"
    log "  ssh -F $run_dir/ssh_config bastion 'grep \"^# \" ~/cds-perf/results/perf-test-cds.out | tail -3'"
    log "Collect the results: run the same command again with -R in place of -D."
    exit 0
fi
wait_for_tests
download_results
