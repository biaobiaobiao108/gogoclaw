#!/usr/bin/env bash
set -euo pipefail

# Use isolated names so the smoke test cannot touch an existing deployment.
tmp_dir="$(mktemp -d)"
name="gogoclaw-smoke-$(basename "$tmp_dir" | tr '[:upper:]' '[:lower:]')-$$"
volume="${name}-data"
if docker container inspect "$name" >/dev/null 2>&1 || docker volume inspect "$volume" >/dev/null 2>&1; then
  rmdir "$tmp_dir"
  echo "Refusing to reuse an existing smoke test container or volume" >&2
  exit 1
fi
cleanup() {
  docker rm -f "$name" >/dev/null 2>&1 || true
  docker volume rm "$volume" >/dev/null 2>&1 || true
  rm -rf -- "$tmp_dir"
}
trap cleanup EXIT

docker volume create "$volume" >/dev/null
docker run -d --name "$name" --read-only --cap-drop ALL \
  --security-opt no-new-privileges:true --tmpfs /tmp:rw,nosuid,nodev,noexec,size=256m \
  -p 127.0.0.1::18800 -v "$volume:/data" \
  "${GOGOCLAW_TEST_IMAGE:-gogoclaw:ci}" gogoclaw-launcher -public -no-browser >/dev/null

port="$(docker port "$name" 18800/tcp | sed -n 's/.*://p' | head -n 1)"
test -n "$port"
base="http://127.0.0.1:$port"
wait_ready() {
  for _ in $(seq 1 60); do
    if curl -fsS "$base/api/auth/status" > "$tmp_dir/status.json"; then return 0; fi
    sleep 1
  done
  docker logs "$name" >&2
  return 1
}
wait_ready
jq -e '.initialized == false' "$tmp_dir/status.json" >/dev/null
token="$(docker logs "$name" 2>&1 | sed -n 's/.*#setup_token=\([^ ]*\).*/\1/p' | head -n 1)"
test -n "$token"
curl -fsS -X POST "$base/api/auth/setup" -H 'Content-Type: application/json' \
  -H "Origin: $base" -H "X-Launcher-Setup-Token: $token" \
  --data '{"password":"container-test-password","confirm":"container-test-password"}' >/dev/null

docker restart "$name" >/dev/null
wait_ready
jq -e '.initialized == true' "$tmp_dir/status.json" >/dev/null
curl -fsS -c "$tmp_dir/cookies" -X POST "$base/api/auth/login" \
  -H 'Content-Type: application/json' -H "Origin: $base" \
  --data '{"password":"container-test-password"}' >/dev/null
curl -fsS -b "$tmp_dir/cookies" "$base/api/auth/status" | jq -e '.authenticated == true' >/dev/null
echo "Container smoke passed: first setup, restart and login"
