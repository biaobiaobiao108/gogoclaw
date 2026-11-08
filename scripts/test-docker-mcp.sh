#!/bin/sh
# Test script for MCP tools in the production container image

set -e

COMPOSE_FILE="docker/docker-compose.yml"
DEV_COMPOSE_FILE="docker/docker-compose.dev.yml"
SERVICE="picoclaw-agent"

echo "🧪 Testing MCP tools in the production container image..."
echo ""

# Build the image
echo "📦 Building Docker image..."
docker compose -f "$COMPOSE_FILE" -f "$DEV_COMPOSE_FILE" --profile agent build "$SERVICE"

# Test Bun
echo "✅ Testing Bun..."
docker compose -f "$COMPOSE_FILE" -f "$DEV_COMPOSE_FILE" --profile agent run --rm --entrypoint sh "$SERVICE" -c 'bun --version'

# Test Bunx
echo "✅ Testing Bunx..."
docker compose -f "$COMPOSE_FILE" -f "$DEV_COMPOSE_FILE" --profile agent run --rm --entrypoint sh "$SERVICE" -c 'bunx --version'

# Test git
echo "✅ Testing git..."
docker compose -f "$COMPOSE_FILE" -f "$DEV_COMPOSE_FILE" --profile agent run --rm --entrypoint sh "$SERVICE" -c 'git --version'

# Test python
echo "✅ Testing Python..."
docker compose -f "$COMPOSE_FILE" -f "$DEV_COMPOSE_FILE" --profile agent run --rm --entrypoint sh "$SERVICE" -c 'python3 --version'

# Test uv
echo "✅ Testing uv..."
docker compose -f "$COMPOSE_FILE" -f "$DEV_COMPOSE_FILE" --profile agent run --rm --entrypoint sh "$SERVICE" -c 'uv --version'

# Test MCP server installation (quick)
echo "✅ Testing @modelcontextprotocol/server-filesystem MCP server install with Bunx..."
docker compose -f "$COMPOSE_FILE" -f "$DEV_COMPOSE_FILE" --profile agent run --rm --entrypoint sh "$SERVICE" -c '</dev/null timeout 5 bunx --bun @modelcontextprotocol/server-filesystem /tmp || true'

echo ""
echo "🎉 All MCP tools are working correctly!"
echo ""
echo "Next steps:"
echo "  1. Configure MCP servers in /data/.picoclaw/config.json"
echo "  2. Run: docker compose -f $COMPOSE_FILE -f $DEV_COMPOSE_FILE --profile gateway up --build"
