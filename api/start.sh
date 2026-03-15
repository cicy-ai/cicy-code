#!/bin/bash
cd "$(dirname "$0")"
# load .env from project root
[ -f ../.env ] && export $(grep -v '^#' ../.env | xargs)
export HOME=${HOST_HOME:-$HOME}
export TERM=xterm-256color
export MYSQL_DSN="root:${MYSQL_ROOT_PASSWORD:-cicy-code}@tcp(localhost:${MYSQL_PORT:-3306})/${MYSQL_DATABASE:-cicy_code}"
export REDIS_ADDR="localhost:${REDIS_PORT:-6379}"
export PORT=${API_PORT:-8008}
export GOMEMLIMIT=2048MiB
exec ./cicy-code-api
