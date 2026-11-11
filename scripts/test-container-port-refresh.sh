#!/usr/bin/env bash
set -euo pipefail

source "$(dirname "${BASH_SOURCE[0]}")/container-smoke-lib.sh"

GOGOCLAW_SMOKE_CONTAINER=port-refresh-test
state_file="$(mktemp)"
trap 'rm -f "$state_file"' EXIT
printf '0\n' > "$state_file"
docker() {
  [[ "$1" == port && "$2" == "$GOGOCLAW_SMOKE_CONTAINER" && "$3" == 18800/tcp ]]
  local call_count
  call_count="$(cat "$state_file")"
  call_count=$((call_count + 1))
  printf '%s\n' "$call_count" > "$state_file"
  case "$call_count" in
    1) printf '%s\n' '0.0.0.0:49123' ;;
    2) printf '%s\n' '127.0.0.1:53177' ;;
    *) return 1 ;;
  esac
}

refresh_container_base
[[ "$GOGOCLAW_SMOKE_BASE" == http://127.0.0.1:49123 ]]
refresh_container_base
[[ "$GOGOCLAW_SMOKE_BASE" == http://127.0.0.1:53177 ]]
[[ "$(cat "$state_file")" == 2 ]]
echo "Container port refresh passed"
