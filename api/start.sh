#!/bin/bash
cd "$(dirname "$0")"
# load .env from project root
[ -f ../.env ] && export $(grep -v '^#' ../.env | xargs)
export HOME=${HOST_HOME:-$HOME}
export TERM=xterm-256color
export MYSQL_DSN="root:cicy-code@tcp(localhost:13306)/cicy_code"
export REDIS_HOST="127.0.0.1"
export REDIS_PORT="16379"
export PORT=14446
export GOMEMLIMIT=2048MiB
exec ./cicy-code-api
