#!/bin/bash
cd "$(dirname "$0")"
pkill -f cicy-code-api 2>/dev/null
sleep 1
export MYSQL_DSN="root:pb200898@tcp(localhost:3306)/cicy_code"
export PORT=14446
exec ./cicy-code-api
