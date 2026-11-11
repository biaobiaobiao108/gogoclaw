#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
tmp_dir="$(mktemp -d)"
trap 'rm -rf -- "$tmp_dir"' EXIT
bin_dir="$tmp_dir/bin"
mkdir -p "$bin_dir"
state_file="$tmp_dir/state"
touch "$state_file"

cat > "$bin_dir/docker" <<'MOCK_DOCKER'
#!/usr/bin/env bash
set -euo pipefail
case "$1 $2" in
  "container inspect"|"volume inspect") exit 1 ;;
  "volume create"|"volume rm"|"rm -f"|"run -d") exit 0 ;;
  "port "*)
    if grep -q '^restarted$' "$SMOKE_STATE"; then
      echo '127.0.0.1:53177'
    else
      echo '127.0.0.1:49123'
    fi
    ;;
  "logs "*)
    if grep -q '^initialized$' "$SMOKE_STATE"; then
      echo 'Launcher ready'
    else
      echo 'First-time setup: open /launcher-setup#setup_token=test-setup-token to create the dashboard password.'
    fi
    ;;
  "restart "*) echo restarted >> "$SMOKE_STATE" ;;
  *) printf 'unexpected docker invocation: %s\n' "$*" >&2; exit 1 ;;
esac
MOCK_DOCKER

cat > "$bin_dir/curl" <<'MOCK_CURL'
#!/usr/bin/env bash
set -euo pipefail
url=""
cookie_file=""
cookie_jar=""
origin=""
args=("$@")
for ((i = 0; i < ${#args[@]}; i++)); do
  arg="${args[$i]}"
  case "$arg" in
    http://*) url="$arg" ;;
    -c) cookie_jar="${args[$((i + 1))]}" ;;
    -b) cookie_file="${args[$((i + 1))]}" ;;
    -H) header="${args[$((i + 1))]}"; [[ "$header" == Origin:* ]] && origin="${header#Origin: }" ;;
  esac
done
[[ -n "$url" ]]
url_base="${url%/api/*}"
if [[ -n "$origin" ]]; then
  [[ "$origin" == "$url_base" ]] || { printf 'Origin %s did not match request base %s\n' "$origin" "$url_base" >&2; exit 1; }
fi
printf '%s %s\n' "${url#http://127.0.0.1:}" "$origin" >> "$SMOKE_REQUESTS"
case "$url" in
  */api/auth/status)
    if grep -q '^initialized$' "$SMOKE_STATE"; then
      if [[ -n "$cookie_file" ]]; then printf '{"initialized":true,"authenticated":true}\n'; else printf '{"initialized":true,"authenticated":false}\n'; fi
    else printf '{"initialized":false,"authenticated":false}\n'; fi
    ;;
  */api/auth/setup)
    grep -q 'test-setup-token' <<< "${args[*]}"
    echo initialized >> "$SMOKE_STATE"
    printf '{"status":"ok"}\n'
    ;;
  */api/auth/login)
    grep -q 'container-test-password' <<< "${args[*]}"
    : > "$cookie_jar"
    printf '{"status":"ok"}\n'
    ;;
  *) printf 'unexpected curl URL: %s\n' "$url" >&2; exit 1 ;;
esac
MOCK_CURL

cat > "$bin_dir/jq" <<'MOCK_JQ'
#!/usr/bin/env bash
set -euo pipefail
expression="$2"
data="$(cat "${3:-/dev/stdin}")"
case "$expression" in
  '.initialized == false') [[ "$data" == *'"initialized":false'* ]] ;;
  '.initialized == true') [[ "$data" == *'"initialized":true'* ]] ;;
  '.authenticated == true') [[ "$data" == *'"authenticated":true'* ]] ;;
  *) exit 1 ;;
esac
MOCK_JQ

chmod +x "$bin_dir/docker" "$bin_dir/curl" "$bin_dir/jq"
export SMOKE_STATE="$state_file" SMOKE_REQUESTS="$tmp_dir/requests"
PATH="$bin_dir:$PATH" bash "$repo_root/scripts/test-container-restart.sh"

grep -q '^49123/api/auth/setup http://127.0.0.1:49123$' "$SMOKE_REQUESTS"
grep -q '^53177/api/auth/login http://127.0.0.1:53177$' "$SMOKE_REQUESTS"
grep -q '^53177/api/auth/status $' "$SMOKE_REQUESTS"
echo "Container restart smoke script passed across port change"
