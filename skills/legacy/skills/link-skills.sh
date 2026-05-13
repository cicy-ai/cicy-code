#!/bin/bash
# 把 ~/Private/skills/bin 链接到目标 agent 的环境
# 用法:
#   bash link-skills.sh                    # 链接到 ~/.local/bin (全局)
#   bash link-skills.sh /path/to/agent/bin # 链接到指定目录

SKILLS_BIN="$(dirname "$(readlink -f "$0")")/bin"
TARGET="${1:-$HOME/.local/bin}"

mkdir -p "$TARGET"

linked=0
skipped=0
for f in "$SKILLS_BIN"/*; do
  [ ! -e "$f" ] && continue
  name=$(basename "$f")
  dest="$TARGET/$name"
  if [ -L "$dest" ] && [ "$(readlink -f "$dest")" = "$(readlink -f "$f")" ]; then
    ((skipped++))
    continue
  fi
  ln -sf "$(readlink -f "$f")" "$dest"
  echo "  linked: $name → $dest"
  ((linked++))
done

echo "Done: $linked linked, $skipped unchanged"
