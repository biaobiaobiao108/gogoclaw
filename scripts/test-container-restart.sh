#!/usr/bin/env bash
set -euo pipefail
source "$(dirname "${BASH_SOURCE[0]}")/container-smoke-lib.sh"

# Use isolated names so the smoke test cannot touch an existing deployment.
tmp_dir="$(mktemp -d)"
GOGOCLAW_SMOKE_CONTAINER="gogoclaw-smoke-$(basename "$tmp_dir" | tr '[:upper:]' '[:lower:]')-$$"
volume="${GOGOCLAW_SMOKE_CONTAINER}-data"
restored_volume="${GOGOCLAW_SMOKE_CONTAINER}-restored"
if docker container inspect "$GOGOCLAW_SMOKE_CONTAINER" >/dev/null 2>&1 || docker volume inspect "$volume" >/dev/null 2>&1 || docker volume inspect "$restored_volume" >/dev/null 2>&1; then
  rmdir "$tmp_dir"
  echo "Refusing to reuse an existing smoke test container or volume" >&2
  exit 1
fi
cleanup() {
  docker rm -f "$GOGOCLAW_SMOKE_CONTAINER" >/dev/null 2>&1 || true
  docker volume rm "$volume" "$restored_volume" >/dev/null 2>&1 || true
  rm -rf -- "$tmp_dir"
}
trap cleanup EXIT

candidate_image="${GOGOCLAW_TEST_IMAGE:-gogoclaw:ci}"
baseline_image="${GOGOCLAW_TEST_BASELINE_IMAGE:-$candidate_image}"

start_launcher() {
  local data_volume="$1" image="$2"
  docker run -d --name "$GOGOCLAW_SMOKE_CONTAINER" --read-only --cap-drop ALL \
    --security-opt no-new-privileges:true --tmpfs /tmp:rw,nosuid,nodev,noexec,size=256m \
    -p 127.0.0.1::18800 -v "$data_volume:/data" \
    "$image" gogoclaw-launcher -public -no-browser >/dev/null
}

docker volume create "$volume" >/dev/null
start_launcher "$volume" "$baseline_image"

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
# Stop all writers before archiving the complete data volume.
docker stop "$GOGOCLAW_SMOKE_CONTAINER" >/dev/null
docker volume create "$restored_volume" >/dev/null
docker run --rm --user 0 --entrypoint sh -v "$volume:/source:ro" -v "$tmp_dir:/backup" \
  "$candidate_image" -c 'tar czf /backup/data.tgz -C /source .'
docker run --rm --user 0 --entrypoint sh -v "$restored_volume:/target" -v "$tmp_dir:/backup:ro" \
  "$candidate_image" -c 'tar xzf /backup/data.tgz -C /target'
docker rm "$GOGOCLAW_SMOKE_CONTAINER" >/dev/null
start_launcher "$restored_volume" "$candidate_image"
wait_ready
jq -e '.initialized == true' "$tmp_dir/status.json" >/dev/null
request /api/auth/login -fsS -c "$tmp_dir/restored-cookies" -X POST \
  -H 'Content-Type: application/json' -H "Origin: $GOGOCLAW_SMOKE_BASE" \
  --data '{"password":"container-test-password","confirm":"container-test-password"}' >/dev/null
request /api/auth/status -fsS -b "$tmp_dir/restored-cookies" | jq -e '.authenticated == true' >/dev/null
request /health -fsS | jq -e '.status == "ok"' >/dev/null

# The old image is started on the pre-upgrade volume. Any schema changes
# made by the new image live only in the restored copy.
if [[ "$baseline_image" != "$candidate_image" ]]; then
  docker stop "$GOGOCLAW_SMOKE_CONTAINER" >/dev/null
  docker rm "$GOGOCLAW_SMOKE_CONTAINER" >/dev/null
  start_launcher "$volume" "$baseline_image"
  wait_ready
  jq -e '.initialized == true' "$tmp_dir/status.json" >/dev/null
  request /api/auth/login -fsS -c "$tmp_dir/rollback-cookies" -X POST \
    -H 'Content-Type: application/json' -H "Origin: $GOGOCLAW_SMOKE_BASE" \
    --data '{"password":"container-test-password"}' >/dev/null
  request /api/auth/status -fsS -b "$tmp_dir/rollback-cookies" | jq -e '.authenticated == true' >/dev/null
fi
echo "Container smoke passed: first setup, restart, backup and restored image login"
