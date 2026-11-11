#!/usr/bin/env bash

refresh_container_base() {
  local published_address
  published_address="$(docker port "$GOGOCLAW_SMOKE_CONTAINER" 18800/tcp | head -n 1)"
  local port="${published_address##*:}"
  if [[ ! "$port" =~ ^[0-9]+$ ]]; then
    echo "could not resolve published launcher port from: $published_address" >&2
    return 1
  fi
  GOGOCLAW_SMOKE_BASE="http://127.0.0.1:$port"
}
