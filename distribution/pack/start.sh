#!/usr/bin/env bash
# Copyright (c) 2026, Choreo Labs LLC. (https://choreolabs.com).
#
# Choreo Labs licenses this file to you under the Apache License,
# Version 2.0 (the "License"); you may not use this file except
# in compliance with the License.
# You may obtain a copy of the License at
#
# http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing,
# software distributed under the License is distributed on an
# "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY
# KIND, either express or implied.  See the License for the
# specific language governing permissions and limitations
# under the License.

#
# Runs the CDD service in the foreground. Ctrl+C stops it.
#
# A thin entry point over `cdd start`, kept in bin/ beside it. It works from any
# directory. Options are passed through:
#
#   bin/start.sh            run CDD
#   bin/start.sh --debug    run CDD, logging at DEBUG level
#
# There is no background mode. Running CDD as a service is the job of whatever
# already runs services — a systemd unit, a container runtime, a process
# supervisor — and each of those runs this in the foreground too.

set -euo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CDD="$HERE/cdd"
[ -x "$CDD" ] || { echo "start: $CDD is missing or not executable" >&2; exit 1; }

exec "$CDD" start "$@"
