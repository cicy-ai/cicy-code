#!/bin/bash
# Usage: mysql-exec "SQL" [database]
# Example: mysql-exec "SELECT * FROM users LIMIT 5;" cicy_code
set -euo pipefail

SQL="${1:?Usage: mysql-exec \"SQL\" [database]}"
DB="${2:-cicy_code}"
PASS=$(grep MYSQL_ROOT_PASSWORD ~/projects/cicy-code/.env 2>/dev/null | cut -d'=' -f2 || echo "")

docker exec -i cicy-mysql sh -c "exec mysql -u root -p'$PASS' $DB -e \"$SQL\"" 2>&1 | grep -v "Warning"
