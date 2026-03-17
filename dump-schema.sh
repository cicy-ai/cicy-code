#!/bin/bash
# 从当前运行的 MySQL 导出 schema + 预启动数据
# Usage: bash dump-schema.sh
set -e

OUT="$(dirname "$0")/schema.sql"
DB="cicy_code"
MYSQL="docker exec cicy-mysql mysql -u root -pcicy-code"
DUMP="docker exec cicy-mysql mysqldump -u root -pcicy-code"

# 导出表结构（不含数据）
$DUMP --no-data "$DB" > "$OUT"

# 追加预启动数据
cat >> "$OUT" << 'EOF'

-- ── 预启动数据 ──

-- 默认 worker
INSERT IGNORE INTO `agent_config` (`pane_id`, `title`, `ttyd_port`, `role`, `workspace`)
VALUES ('w-10001:main.0', 'Main', 10001, 'master', '~/workers/w-10001');

-- worker 编号从 20000 起
INSERT IGNORE INTO `global_vars` (`key_name`, `value`) VALUES ('worker_index', '20000');
EOF

echo "✅ schema.sql 已更新: $OUT"
