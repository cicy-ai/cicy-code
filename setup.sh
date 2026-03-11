#!/bin/bash
set -e

# Generate .env
cat > .env <<EOF
HOST_HOME=$HOME
HOST_UID=$(id -u)
HOST_GID=$(id -g)
EOF
echo "✅ Generated .env"
cat .env

# Start services
echo ""
echo "🚀 Starting services..."
docker compose up -d

echo ""
echo "✅ All services started!"
echo "   IDE:         http://localhost:6902"
echo "   API:         http://localhost:14446"
echo "   code-server: http://localhost:14446/code"
