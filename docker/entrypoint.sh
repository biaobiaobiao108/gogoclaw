#!/bin/sh
set -eu

if [ "$#" -eq 0 ]; then
    set -- gateway
fi

case "$1" in
    gateway)
        shift
        if [ ! -f "${HOME}/.picoclaw/config.json" ]; then
            if [ "${PICOCLAW_BOOTSTRAP:-0}" = "1" ]; then
                picoclaw onboard
            fi
            if [ ! -f "${HOME}/.picoclaw/config.json" ]; then
                echo "error: ${HOME}/.picoclaw/config.json is missing." >&2
                echo "Run 'docker compose -f docker/docker-compose.yml --profile gateway run --rm --entrypoint picoclaw picoclaw-gateway onboard' and restart." >&2
                exit 78
            fi
        fi
        rm -f "${HOME}/.picoclaw/.picoclaw.pid"
        exec picoclaw gateway "$@"
        ;;
    picoclaw)
        exec "$@"
        ;;
    picoclaw-launcher)
        rm -f "${HOME}/.picoclaw/.picoclaw.pid"
        exec "$@"
        ;;
    agent|auth|config|cron|mcp|migrate|model|onboard|skills|status|version)
        exec picoclaw "$@"
        ;;
    *)
        exec "$@"
        ;;
esac
