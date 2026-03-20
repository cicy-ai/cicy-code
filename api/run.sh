#!/bin/bash
ENV_FILE="/home/w3c_offical/projects/cicy-api/.env"
if [ -f "$ENV_FILE" ]; then
    set -a; source "$ENV_FILE"; set +a
fi
export MYSQL_DSN="${MYSQL_DSN:-root:cicy-code@tcp(localhost:3306)/cicy_code}"
export REDIS_ADDR="${REDIS_ADDR:-localhost:6379}"
export PORT="${PORT:-8008}"
export HOME=/home/w3c_offical
export TERM=xterm-256color
/home/w3c_offical/projects/cicy-code/api/cicy-code-api --saas --public
