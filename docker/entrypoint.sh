#!/bin/sh
set -eu

if [ "$#" -eq 0 ]; then
    set -- gateway
fi

case "$1" in
    gateway)
        shift
        if [ ! -f "${HOME}/.gogoclaw/config.json" ]; then
            if [ "${GOGOCLAW_BOOTSTRAP:-0}" = "1" ]; then
                gogoclaw onboard
            fi
            if [ ! -f "${HOME}/.gogoclaw/config.json" ]; then
                echo "error: ${HOME}/.gogoclaw/config.json is missing." >&2
                echo "Run 'docker compose -f docker/docker-compose.yml --profile gateway run --rm --entrypoint gogoclaw gogoclaw-gateway onboard' and restart." >&2
                exit 78
            fi
        fi
        exec gogoclaw gateway "$@"
        ;;
    gogoclaw)
        exec "$@"
        ;;
    gogoclaw-launcher)
        exec "$@"
        ;;
    agent|auth|config|cron|mcp|migrate|model|onboard|skills|status|version)
        exec gogoclaw "$@"
        ;;
    *)
        exec "$@"
        ;;
esac
