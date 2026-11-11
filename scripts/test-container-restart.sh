#!/usr/bin/env bash
set -euo pipefail
source "$(dirname "${BASH_SOURCE[0]}")/container-smoke-lib.sh"

# Use isolated names so the smoke test cannot touch an existing deployment.
tmp_dir="$(mktemp -d)"
GOGOCLAW_SMOKE_CONTAINER="gogoclaw-smoke-$(basename "$tmp_dir" | tr '[:upper:]' '[:lower:]')-$$"
volume="${GOGOCLAW_SMOKE_CONTAINER}-data"
if docker container inspect "$GOGOCLAW_SMOKE_CONTAINER" >/dev/null 2>&1 || docker volume inspect "$volume" >/dev/null 2>&1; then
  rmdir "$tmp_dir"
  echo "Refusing to reuse an existing smoke test container or volume" >&2
  exit 1
fi
cleanup() {
  docker rm -f "$GOGOCLAW_SMOKE_CONTAINER" >/dev/null 2>&1 || true
  docker volume rm "$volume" >/dev/null 2>&1 || true
  rm -rf -- "$tmp_dir"
}
trap cleanup EXIT

docker volume create "$volume" >/dev/null
docker run -d --name "$GOGOCLAW_SMOKE_CONTAINER" --read-only --cap-drop ALL \
  --security-opt no-new-privileges:true --tmpfs /tmp:rw,nosuid,nodev,noexec,size=256m \
  -p 127.0.0.1::18800 \
  -v "$volume:/data" \
  "${GOGOCLAW_TEST_IMAGE:-gogoclaw:ci}" gogoclaw-launcher -public -no-browser >/dev/null

request() {
  local path="$1"
  shift
  refresh_container_base
  curl "$@" "$GOGOCLAW_SMOKE_BASE$path"
}
wait_ready() {
  for _ in $(seq 1 60); do
    if request /api/auth/status -fs > "$tmp_dir/status.json" 2>/dev/null; then return 0; fi
    sleep 1
  done
  docker logs "$GOGOCLAW_SMOKE_CONTAINER" >&2
  return 1
}
wait_ready
jq -e '.initialized == false' "$tmp_dir/status.json" >/dev/null
token="$(docker logs "$GOGOCLAW_SMOKE_CONTAINER" 2>&1 | sed -n 's/.*#setup_token=\([^ ]*\).*/\1/p' | head -n 1)"
test -n "$token"
refresh_container_base
request /api/auth/setup -fsS -X POST -H 'Content-Type: application/json' \
  -H "Origin: $GOGOCLAW_SMOKE_BASE" -H "X-Launcher-Setup-Token: $token" \
  --data '{"password":"container-test-password","confirm":"container-test-password"}' >/dev/null

docker restart "$GOGOCLAW_SMOKE_CONTAINER" >/dev/null
wait_ready
jq -e '.initialized == true' "$tmp_dir/status.json" >/dev/null
request /api/auth/login -fsS -c "$tmp_dir/cookies" -X POST \
  -H 'Content-Type: application/json' -H "Origin: $GOGOCLAW_SMOKE_BASE" \
  --data '{"password":"container-test-password"}' >/dev/null
request /api/auth/status -fsS -b "$tmp_dir/cookies" | jq -e '.authenticated == true' >/dev/null
echo "Container smoke passed: first setup, restart and login"
